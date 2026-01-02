package mon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cneill/mon/pkg/mon/files"
	"github.com/cneill/mon/pkg/mon/git"
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

	fileMonitor  *files.Monitor
	gitMonitor   *git.Monitor
	writeLimiter *rate.Limiter

	displayChan chan struct{}
}

func New(opts *Opts) (*Mon, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("failed to configure mon: %w", err)
	}

	fileMonitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  opts.ProjectDir,
		WatchRoot: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set up file monitor: %w", err)
	}

	gitMonitor, err := git.NewMonitor(&git.MonitorOpts{
		RootPath: opts.ProjectDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set up git monitor: %w", err)
	}

	// Get initial unstaged changes count
	gitMonitor.Update()

	mon := &Mon{
		Opts: opts,

		writeLimiter: rate.NewLimiter(3, 1),
		fileMonitor:  fileMonitor,
		gitMonitor:   gitMonitor,

		displayChan: make(chan struct{}),
	}

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
