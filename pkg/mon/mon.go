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

		snapshot := m.getStatusSnapshot()

		fmt.Printf("\r%s", snapshot.String())
		os.Stdout.Sync()
	}
}

func (m *Mon) getStatusSnapshot() *statusSnapshot {
	return &statusSnapshot{
		FilesCreated: atomic.LoadInt64(&m.filesCreated),
		FilesDeleted: atomic.LoadInt64(&m.filesDeleted),
		Commits:      atomic.LoadInt64(&m.commits),
		LinesAdded:   atomic.LoadInt64(&m.linesAdded),
		LinesDeleted: atomic.LoadInt64(&m.linesDeleted),
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
	builder.WriteString(color.YellowString("%d", s.Commits))
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
	builder.WriteString(color.RedString(strconv.FormatInt(s.FilesCreated, 10) + " deleted"))
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
