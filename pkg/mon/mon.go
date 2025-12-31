package mon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/cneill/mon/pkg/git"
	"github.com/cneill/mon/pkg/mon/files"
	gogit "github.com/go-git/go-git/v5"
	"golang.org/x/time/rate"
)

type Opts struct {
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
	FileMonitor  *files.FileMonitor
	writeLimiter *rate.Limiter

	displayChan chan struct{}

	mutex             sync.Mutex
	initialHash       string
	lastProcessedHash string
	commits           atomic.Int64
	linesAdded        atomic.Int64
	linesDeleted      atomic.Int64
	unstagedChanges   atomic.Int64
	gitLogPath        string
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

	fileMonitor, err := files.NewFileMonitor(&files.FileMonitorOpts{
		RootPath: opts.ProjectDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set up file monitor: %w", err)
	}

	if err := fileMonitor.WatchFile(gitLogPath); err != nil {
		return nil, fmt.Errorf("failed to set up monitoring for git log file: %w", err)
	}

	mon := &Mon{
		Opts: opts,

		repo:         repo,
		writeLimiter: rate.NewLimiter(3, 1),
		FileMonitor:  fileMonitor,

		displayChan: make(chan struct{}),

		initialHash:       initialHash,
		lastProcessedHash: initialHash,
		gitLogPath:        gitLogPath,
	}

	// Get initial unstaged changes count
	mon.updateUnstagedChanges()

	return mon, nil
}

func (m *Mon) Run(ctx context.Context) error {
	go m.handleFSEvents(ctx)

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
