package mon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cneill/mon/pkg/git"
	"github.com/fsnotify/fsnotify"
	gogit "github.com/go-git/go-git/v5"
)

type Opts struct {
	GitWatch   bool
	NoColor    bool
	ProjectDir string
}

func (o *Opts) OK() error {
	if o.ProjectDir == "" {
		return fmt.Errorf("must supply project dir")
	}

	if _, err := os.Stat(o.ProjectDir); err != nil {
		return fmt.Errorf("failed to stat project dir: %w", err)
	}

	return nil
}

type Mon struct {
	*Opts

	repo *gogit.Repository

	mutex             sync.Mutex
	initialHash       string
	lastProcessedHash string
	filesCreated      atomic.Int64
	filesDeleted      atomic.Int64
	commits           atomic.Int64
	linesAdded        atomic.Int64
	linesDeleted      atomic.Int64
	gitLogPath        string
	initialFiles      sync.Map // Tracks initial files on start (read-only after init)
	newFiles          sync.Map // Tracks files created after initialization
	pendingDeletes    sync.Map // key: string path, value: pendingDelete
	deleteTimeout     time.Duration
}

func New(opts *Opts) (*Mon, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("failed to configure mon: %w", err)
	}

	repo, err := git.OpenGitRepo(opts.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repo in project dir %q: %w", opts.ProjectDir, err)
	}

	initialHash, err := git.GetHEADSHA(repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get initial git HEAD SHA: %w", err)
	}

	gitLogPath := filepath.Join(opts.ProjectDir, ".git", "logs", "HEAD")
	if _, err := os.Stat(gitLogPath); err != nil {
		return nil, fmt.Errorf("git logs not found at %s", gitLogPath)
	}

	mon := &Mon{
		Opts: opts,

		repo: repo,

		initialHash:       initialHash,
		lastProcessedHash: initialHash,
		gitLogPath:        gitLogPath,
		deleteTimeout:     250 * time.Millisecond,
	}

	if err := mon.populateInitialFiles(); err != nil {
		return nil, fmt.Errorf("failed to populate initial project files: %w", err)
	}

	return mon, nil
}

func (m *Mon) Run(_ context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	if err := m.addRecursiveWatchesForDir(watcher, m.ProjectDir); err != nil {
		return err
	}

	if err := watcher.Add(m.gitLogPath); err != nil {
		return fmt.Errorf("failed to watch %s: %w", m.gitLogPath, err)
	}

	displayCh := make(chan struct{}, 10)

	go m.handleEvents(watcher, displayCh)

	go m.processPendingDeletes(displayCh)

	go m.displayLoop(displayCh)

	m.triggerDisplay(displayCh)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	snapshot := m.getStatusSnapshot()
	fmt.Println("\n" + snapshot.Final())

	return nil
}

type pendingDelete struct {
	timestamp  time.Time
	wasNewFile bool // true if file was in newFiles, false if in initialFiles
}

func (m *Mon) populateInitialFiles() error {
	// Scan initial files (non-dirs, skip .git)
	scanErr := filepath.WalkDir(m.ProjectDir, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if de.IsDir() || strings.Contains(path, ".git") {
			if de.IsDir() && filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}

			return nil
		}

		m.initialFiles.Store(path, struct{}{})

		return nil
	})
	if scanErr != nil {
		return fmt.Errorf("failed to scan initial files: %w", scanErr)
	}

	return nil
}

func (m *Mon) processPendingDeletes(displayCh chan<- struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		m.pendingDeletes.Range(func(key, value any) bool {
			pd, ok := value.(pendingDelete)
			if !ok {
				m.pendingDeletes.Delete(key)
				return true
			}

			if time.Since(pd.timestamp) > m.deleteTimeout {
				m.pendingDeletes.Delete(key)

				if pd.wasNewFile {
					// New file deleted: decrement created count (net zero)
					m.filesCreated.Add(-1)
					slog.Debug("confirmed delete (new file, decrement created)", "name", key)
				} else {
					// Initial file deleted: increment deleted count
					m.filesDeleted.Add(1)
					slog.Debug("confirmed delete (initial file)", "name", key)
				}

				m.triggerDisplay(displayCh)
			}

			return true
		})
	}
}

func (m *Mon) addRecursiveWatchesForDir(watcher *fsnotify.Watcher, dir string) error {
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		if strings.Contains(path, ".git") {
			return filepath.SkipDir
		}

		if err := watcher.Add(path); err != nil {
			return fmt.Errorf("failed to add watcher for directory %q: %w", path, err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to recursively add dir watches: %w", err)
	}

	return nil
}

func (m *Mon) processGitChange(displayCh chan<- struct{}) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	newHash, err := git.GetHEADSHA(m.repo)
	if err != nil {
		slog.Error("failed to get new git SHA", "error", err)
	}

	countCmd := exec.Command("git", "-C", m.ProjectDir, "rev-list", "--count", m.initialHash+".."+newHash)

	countBytes, err := countCmd.Output()
	if err != nil {
		return
	}

	commitsTotalStr := strings.TrimSpace(string(countBytes))

	commitsTotal, err := strconv.ParseInt(commitsTotalStr, 10, 64)
	if err != nil {
		return
	}

	m.commits.Store(commitsTotal)

	diffCmd := exec.Command("git", "-C", m.ProjectDir, "diff", "--shortstat", m.initialHash+".."+newHash)

	diffBytes, err := diffCmd.Output()
	if err != nil {
		return
	}

	added, deleted := m.parseShortstat(string(diffBytes))
	m.linesAdded.Store(added)
	m.linesDeleted.Store(deleted)

	m.lastProcessedHash = newHash
	m.triggerDisplay(displayCh)
}

func (m *Mon) parseShortstat(stat string) (int64, int64) {
	stat = strings.TrimSpace(stat)
	if stat == "" {
		return 0, 0
	}

	insertionsIdx := strings.Index(stat, "insertions(+)")
	if insertionsIdx == -1 {
		return 0, 0
	}

	addStr := stat[strings.LastIndex(stat[:insertionsIdx], " ")+1 : insertionsIdx]
	addStr = strings.TrimSpace(addStr)
	added, _ := strconv.ParseInt(addStr, 10, 64)

	deletionsIdx := strings.Index(stat, "deletions(-)")
	if deletionsIdx == -1 {
		return added, 0
	}

	delStr := stat[insertionsIdx+len("insertions(+)") : deletionsIdx]
	delStr = strings.TrimSpace(delStr)
	deleted, _ := strconv.ParseInt(delStr, 10, 64)

	return added, deleted
}
