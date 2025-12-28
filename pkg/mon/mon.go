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

	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
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

	return nil
}

type Mon struct {
	*Opts

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

	cmd := exec.Command("git", "-C", opts.ProjectDir, "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s is not a git repository", opts.ProjectDir)
	}

	hashCmd := exec.Command("git", "-C", opts.ProjectDir, "rev-parse", "HEAD")

	hashBytes, err := hashCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get initial HEAD: %w", err)
	}

	initialHash := strings.TrimSpace(string(hashBytes))

	gitLogPath := filepath.Join(opts.ProjectDir, ".git", "logs", "HEAD")

	mon := &Mon{
		Opts:              opts,
		initialHash:       initialHash,
		lastProcessedHash: initialHash,
		gitLogPath:        gitLogPath,
		deleteTimeout:     250 * time.Millisecond,
	}

	// Scan initial files (non-dirs, skip .git)
	scanErr := filepath.WalkDir(opts.ProjectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || strings.Contains(path, ".git") {
			if d.IsDir() && filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}

			return nil
		}

		mon.initialFiles.Store(path, struct{}{})

		return nil
	})
	if scanErr != nil {
		return nil, fmt.Errorf("failed to scan initial files: %w", scanErr)
	}

	return mon, nil
}

func (m *Mon) Run(_ context.Context) error {
	if _, err := os.Stat(m.gitLogPath); err != nil {
		return fmt.Errorf("git logs not found at %s", m.gitLogPath)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	if err := m.addRecursiveWatches(watcher); err != nil {
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

	m.printFinalStats()

	return nil
}

func (m *Mon) addRecursiveWatches(watcher *fsnotify.Watcher) error {
	return filepath.WalkDir(m.ProjectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		if filepath.Base(path) == ".git" {
			return filepath.SkipDir
		}

		if err := watcher.Add(path); err != nil {
			return fmt.Errorf("failed to watch directory %q: %w", path, err)
		}

		return nil
	})
}

type pendingDelete struct {
	timestamp  time.Time
	wasNewFile bool // true if file was in newFiles, false if in initialFiles
}

func (m *Mon) handleEvents(watcher *fsnotify.Watcher, displayCh chan<- struct{}) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if m.ignoreEvent(event) {
				continue
			}

			if event.Name == m.gitLogPath && (event.Op&(fsnotify.Write|fsnotify.Chmod) != 0) {
				go m.processGitChange(displayCh)
				continue
			}

			switch {
			case event.Op&fsnotify.Create != 0:
				// Check for matching pending delete
				if pdI, pending := m.pendingDeletes.LoadAndDelete(event.Name); pending {
					pd := pdI.(pendingDelete)
					if pd.wasNewFile {
						// Restore to newFiles, no count change needed
						m.newFiles.Store(event.Name, struct{}{})
					} else {
						// Restore to initialFiles, no count change needed
						m.initialFiles.Store(event.Name, struct{}{})
					}

					slog.Debug("ignored delete+create pair", "name", event.Name)

					continue
				}

				// Only count if not already tracked in either map
				_, inInitial := m.initialFiles.Load(event.Name)
				_, inNew := m.newFiles.Load(event.Name)

				if !inInitial && !inNew {
					slog.Debug("added file", "name", event.Name)
					m.filesCreated.Add(1)
					m.newFiles.Store(event.Name, struct{}{})
					m.triggerDisplay(displayCh)
				}

				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					m.addRecursiveWatchesForDir(watcher, event.Name)
				}

			case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
				// Check if in newFiles first
				if _, exists := m.newFiles.LoadAndDelete(event.Name); exists {
					slog.Debug("pending delete (new file)", "name", event.Name)
					m.pendingDeletes.Store(event.Name, pendingDelete{
						timestamp:  time.Now(),
						wasNewFile: true,
					})

					continue
				}

				// Check if in initialFiles
				if _, exists := m.initialFiles.LoadAndDelete(event.Name); exists {
					slog.Debug("pending delete (initial file)", "name", event.Name)
					m.pendingDeletes.Store(event.Name, pendingDelete{
						timestamp:  time.Now(),
						wasNewFile: false,
					})
				}
			}

		case <-watcher.Errors:
		}
	}
}

func (m *Mon) ignoreEvent(event fsnotify.Event) bool {
	if strings.HasPrefix(event.Name, ".git") {
		return true
	}

	// Ignore editor temp files: backups (~, .swp), swap (numeric names)
	base := filepath.Base(event.Name)
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") || isNumeric(base) {
		return true
	}

	return false
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
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() && !strings.Contains(path, ".git") {
			watcher.Add(path)
		}

		return nil
	})
}

func (m *Mon) processGitChange(displayCh chan<- struct{}) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	cmd := exec.Command("git", "-C", m.ProjectDir, "rev-parse", "HEAD")

	hashBytes, err := cmd.Output()
	if err != nil {
		return
	}

	newHash := strings.TrimSpace(string(hashBytes))
	if newHash == m.lastProcessedHash {
		return
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

func (m *Mon) triggerDisplay(displayCh chan<- struct{}) {
	select {
	case displayCh <- struct{}{}:
	default:
	}
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

func isNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (m *Mon) displayLoop(displayCh <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-displayCh:
		case <-ticker.C:
		}

		snapshot := m.getStatusSnapshot()

		fmt.Printf("\r%s", snapshot.String())
		os.Stdout.Sync()
	}
}

func (m *Mon) getStatusSnapshot() *statusSnapshot {
	return &statusSnapshot{
		FilesCreated: m.filesCreated.Load(),
		FilesDeleted: m.filesDeleted.Load(),
		Commits:      m.commits.Load(),
		LinesAdded:   m.linesAdded.Load(),
		LinesDeleted: m.linesDeleted.Load(),
	}
}

type statusSnapshot struct {
	FilesCreated int64
	FilesDeleted int64
	Commits      int64
	LinesAdded   int64
	LinesDeleted int64
}

func (s *statusSnapshot) String() string {
	builder := &strings.Builder{}
	builder.WriteString("Files: ")
	builder.WriteString(color.GreenString("+" + strconv.FormatInt(s.FilesCreated, 10)))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString("-" + strconv.FormatInt(s.FilesDeleted, 10)))
	builder.WriteString(" || Commits: ")
	builder.WriteString(color.YellowString(strconv.FormatInt(s.Commits, 10)))
	builder.WriteString(" || Lines: ")
	builder.WriteString(color.GreenString("+" + strconv.FormatInt(s.LinesAdded, 10)))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString("-" + strconv.FormatInt(s.LinesDeleted, 10)))

	return builder.String()
}

func (s *statusSnapshot) Final() string {
	builder := &strings.Builder{}

	builder.WriteString("Session stats:\n")

	builder.WriteString(" - Files: ")
	builder.WriteString(color.GreenString(strconv.FormatInt(s.FilesCreated, 10) + " created"))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString(strconv.FormatInt(s.FilesDeleted, 10) + " deleted"))
	builder.WriteRune('\n')

	builder.WriteString(" - Commits: " + color.YellowString("+"+strconv.FormatInt(s.Commits, 10)) + "\n")

	builder.WriteString(" - Lines: ")
	builder.WriteString(color.GreenString(strconv.FormatInt(s.LinesAdded, 10) + " added"))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString(strconv.FormatInt(s.LinesDeleted, 10) + " deleted"))
	builder.WriteRune('\n')

	return builder.String()
}

func (m *Mon) printFinalStats() {
	snapshot := m.getStatusSnapshot()
	fmt.Println("\n" + snapshot.Final())
}
