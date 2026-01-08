package mon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cneill/mon/pkg/files"
	"github.com/cneill/mon/pkg/git"
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
	startTime   time.Time
	lastWrite   time.Time
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
	gitMonitor.Update(context.Background())

	mon := &Mon{
		Opts: opts,

		writeLimiter: rate.NewLimiter(3, 1),
		fileMonitor:  fileMonitor,
		gitMonitor:   gitMonitor,

		startTime:   time.Now(),
		displayChan: make(chan struct{}),
	}

	return mon, nil
}

func (m *Mon) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go m.fileMonitor.Run(ctx)
	defer m.fileMonitor.Close()

	go m.gitMonitor.Run(ctx)
	defer m.gitMonitor.Close()

	go m.handleEvents(ctx)

	go m.displayLoop(ctx)

	m.triggerDisplay()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigChan:
		slog.Debug("Got SIGINT/SIGTERM")
	case <-ctx.Done():
		slog.Debug("Context cancelled")
	}

	cancel() // Cancel context first so goroutines can exit before Close() waits on them

	snapshot := m.getStatusSnapshot(true)
	fmt.Println(clearLine + snapshot.Final())

	return nil
}

func (m *Mon) Teardown() {
	close(m.displayChan)
}

func (m *Mon) handleEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-m.fileMonitor.Events:
			if !ok {
				slog.Info("file monitor shut down")
				return
			}

			m.handleFileEvent(ctx, event)

		case event, ok := <-m.gitMonitor.GitEvents:
			if !ok {
				slog.Info("git monitor shut down")
				return
			}

			if event.Type == git.EventTypeNewCommit {
				m.triggerDisplay()
			}
		}
	}
}

func (m *Mon) handleFileEvent(ctx context.Context, event files.Event) {
	switch event.Type() { //nolint:exhaustive
	case files.EventTypeCreate, files.EventTypeRemove, files.EventTypeRename:
		go m.triggerDisplay()
	case files.EventTypeWrite:
		m.lastWrite = time.Now()

		time.Sleep(time.Millisecond * 250) // allow write+delete pairs to settle before checking

		if m.writeLimiter.Allow() {
			m.writeLimiter.Reserve()

			select {
			case <-ctx.Done():
				return
			case m.gitMonitor.FileEvents <- event:
			}
		}
	}
}
