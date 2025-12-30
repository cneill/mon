package mon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cneill/mon/pkg/git"
	"github.com/fsnotify/fsnotify"
	gogit "github.com/go-git/go-git/v5"
	"golang.org/x/time/rate"
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

	repo         *gogit.Repository
	watcher      *fsnotify.Watcher
	writeLimiter *rate.Limiter

	displayChan chan struct{}

	mutex             sync.Mutex
	initialHash       string
	lastProcessedHash string
	filesCreated      atomic.Int64 // TODO: track with git?
	filesDeleted      atomic.Int64 // TODO: track with git?
	commits           atomic.Int64
	linesAdded        atomic.Int64
	linesDeleted      atomic.Int64
	unstagedChanges   atomic.Int64
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

	gitLogPath, err := filepath.Abs(filepath.Join(opts.ProjectDir, ".git", "logs", "HEAD"))
	if err != nil {
		return nil, fmt.Errorf("failed to get git log path: %w", err)
	}

	if _, err := os.Stat(gitLogPath); err != nil {
		return nil, fmt.Errorf("git logs not found at %s", gitLogPath)
	}

	mon := &Mon{
		Opts: opts,

		repo:         repo,
		writeLimiter: rate.NewLimiter(3, 1),

		displayChan: make(chan struct{}),

		initialHash:       initialHash,
		lastProcessedHash: initialHash,
		gitLogPath:        gitLogPath,
		deleteTimeout:     250 * time.Millisecond,
	}

	if err := mon.populateInitialFiles(); err != nil {
		return nil, fmt.Errorf("failed to populate initial project files: %w", err)
	}

	if err := mon.setupWatcher(); err != nil {
		return nil, err
	}

	// Get initial unstaged changes count
	mon.updateUnstagedChanges()

	return mon, nil
}

func (m *Mon) Run(_ context.Context) error {
	go m.handleEvents()

	go m.processPendingDeletes()

	go m.displayLoop()

	m.triggerDisplay()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	snapshot := m.getStatusSnapshot(true)
	fmt.Println(clearLine + snapshot.Final())

	return nil
}

func (m *Mon) Teardown() {
	if m.watcher != nil {
		if err := m.watcher.Close(); err != nil {
			slog.Error("failed to close watcher", "error", err)
		}
	}
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

func (m *Mon) setupWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	m.watcher = watcher

	if err := m.addRecursiveWatchesForDir(m.ProjectDir); err != nil {
		return err
	}

	if err := watcher.Add(m.gitLogPath); err != nil {
		return fmt.Errorf("failed to watch %s: %w", m.gitLogPath, err)
	}

	return nil
}

func (m *Mon) processPendingDeletes() {
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

				m.triggerDisplay()
			}

			return true
		})
	}
}

func (m *Mon) addRecursiveWatchesForDir(dir string) error {
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

		if err := m.watcher.Add(path); err != nil {
			return fmt.Errorf("failed to add watcher for directory %q: %w", path, err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to recursively add dir watches: %w", err)
	}

	return nil
}

func (m *Mon) processGitChange() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	slog.Debug("Processing git change")

	commits, err := git.CommitsSince(m.repo, m.initialHash)
	if err != nil {
		slog.Error("failed to list commits since initialization", "error", err)
	}

	m.commits.Store(int64(len(commits)))

	newHash, err := git.GetHEADSHA(m.repo)
	if err != nil {
		slog.Error("failed to get new git SHA", "error", err)
	}

	patch, err := git.PatchSince(m.repo, m.initialHash)
	if err != nil {
		slog.Error("failed to generate patch", "initial_hash", m.initialHash, "head_hash", newHash)
	}

	adds, deletes := git.PatchAddsDeletes(patch)
	m.linesAdded.Store(adds)
	m.linesDeleted.Store(deletes)

	m.updateUnstagedChanges()

	m.lastProcessedHash = newHash
	m.triggerDisplay()
}

func (m *Mon) updateUnstagedChanges() {
	unstagedCount, err := git.UnstagedChangeCount(m.repo)
	if err != nil {
		slog.Error("failed to check unstaged changes", "error", err)
		return
	}

	m.unstagedChanges.Store(unstagedCount)
}
