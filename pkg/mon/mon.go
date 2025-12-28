package mon

import (
	"context"
	"fmt"
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
	mu                sync.Mutex
	initialHash       string
	lastProcessedHash string
	filesCreated      int64
	filesDeleted      int64
	commits           int64
	linesAdded        int64
	linesDeleted      int64
	gitLogPath        string
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
		if d.IsDir() {
			if filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}
			if err := watcher.Add(path); err != nil {
			}
		}
		return nil
	})
}

func (m *Mon) handleEvents(watcher *fsnotify.Watcher, displayCh chan<- struct{}) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Name == m.gitLogPath && (event.Op&(fsnotify.Write|fsnotify.Chmod) != 0) {
				go m.processGitChange(displayCh)
				continue
			}

			if strings.Contains(event.Name, ".git") {
				continue
			}

			switch {
			case event.Op&fsnotify.Create != 0:
				atomic.AddInt64(&m.filesCreated, 1)
				m.triggerDisplay(displayCh)
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					m.addRecursiveWatchesForDir(watcher, event.Name)
				}
			case event.Op&fsnotify.Remove != 0:
				atomic.AddInt64(&m.filesDeleted, 1)
				m.triggerDisplay(displayCh)
			case event.Op&fsnotify.Rename != 0:
				atomic.AddInt64(&m.filesCreated, 1)
				atomic.AddInt64(&m.filesDeleted, 1)
				m.triggerDisplay(displayCh)
			}

		case <-watcher.Errors:
		}
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
	m.mu.Lock()
	defer m.mu.Unlock()

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
	atomic.StoreInt64(&m.commits, commitsTotal)

	diffCmd := exec.Command("git", "-C", m.ProjectDir, "diff", "--shortstat", m.initialHash+".."+newHash)
	diffBytes, err := diffCmd.Output()
	if err != nil {
		return
	}
	added, deleted := m.parseShortstat(string(diffBytes))
	atomic.StoreInt64(&m.linesAdded, added)
	atomic.StoreInt64(&m.linesDeleted, deleted)

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

	i := strings.Index(stat, "insertions(+)")
	if i == -1 {
		return 0, 0
	}
	addStr := stat[strings.LastIndex(stat[:i], " ")+1 : i]
	addStr = strings.TrimSpace(addStr)
	added, _ := strconv.ParseInt(addStr, 10, 64)

	d := strings.Index(stat, "deletions(-)")
	if d == -1 {
		return added, 0
	}
	delStr := stat[i+len("insertions(+)") : d]
	delStr = strings.TrimSpace(delStr)
	deleted, _ := strconv.ParseInt(delStr, 10, 64)

	return added, deleted
}

func (m *Mon) displayLoop(displayCh <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-displayCh:
		case <-ticker.C:
		}

		created := atomic.LoadInt64(&m.filesCreated)
		fdeleted := atomic.LoadInt64(&m.filesDeleted)
		commits := atomic.LoadInt64(&m.commits)
		ladded := atomic.LoadInt64(&m.linesAdded)
		ldeleted := atomic.LoadInt64(&m.linesDeleted)

		status := m.buildStatus(created, fdeleted, commits, ladded, ldeleted)
		fmt.Printf("\r%s", status)
		os.Stdout.Sync()
	}
}

func (m *Mon) buildStatus(created, fdeleted, commits, ladded, ldeleted int64) string {
	if m.NoColor {
		return fmt.Sprintf("Files: +%d/-%d | Commits: %d | Lines: +%d/- %d", created, fdeleted, commits, ladded, ldeleted)
	}

	const green = "\x1b[32m"
	const red = "\x1b[31m"
	const yellow = "\x1b[33m"
	const reset = "\x1b[0m"

	return fmt.Sprintf("Files: %s+%d%s/%s-%d%s | %sCommits: %d%s | %s+%d%s/%s-%d%s", green, created, reset, red, fdeleted, reset, yellow, commits, reset, green, ladded, reset, red, ldeleted, reset)
}

func (m *Mon) printFinalStats() {
	fmt.Println()

	created := atomic.LoadInt64(&m.filesCreated)
	fdeleted := atomic.LoadInt64(&m.filesDeleted)
	commits := atomic.LoadInt64(&m.commits)
	ladded := atomic.LoadInt64(&m.linesAdded)
	ldeleted := atomic.LoadInt64(&m.linesDeleted)

	if m.NoColor {
		fmt.Printf("Session ended:\n - Files: +%d created, -%d deleted\n - Commits: %d\n - Lines: +%d added, -%d deleted\n", created, fdeleted, commits, ladded, ldeleted)
	} else {
		const green = "\x1b[32m"
		const red = "\x1b[31m"
		const yellow = "\x1b[33m"
		const reset = "\x1b[0m"
		fmt.Printf("Session ended:\n - Files: %s+%d%s created, %s-%d%s deleted\n - %sCommits: %d%s\n - Lines: %s+%d%s added, %s-%d%s deleted\n", green, created, reset, red, fdeleted, reset, yellow, commits, reset, green, ladded, reset, red, ldeleted, reset)
	}
}
