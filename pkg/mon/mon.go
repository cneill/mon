package mon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cneill/mon/pkg/files"
	"github.com/cneill/mon/pkg/git"
	"github.com/cneill/mon/pkg/listeners"
	"golang.org/x/time/rate"
)

type Opts struct {
	NoColor    bool
	ProjectDir string
	Listeners  []listeners.Listener

	DetailsOpts *DetailsOpts
}

func (o *Opts) OK() error {
	if o.ProjectDir == "" {
		return fmt.Errorf("must supply project dir")
	}

	if _, err := os.Stat(o.ProjectDir); err != nil {
		return fmt.Errorf("failed to stat project dir: %w", err)
	}

	if o.DetailsOpts == nil {
		return fmt.Errorf("must supply details options")
	}

	return nil
}

type DetailsOpts struct {
	ShowAllFiles bool
}

type Mon struct {
	*Opts

	fileMonitor  *files.Monitor
	gitMonitor   *git.Monitor
	writeLimiter *rate.Limiter

	displayChan chan struct{}
	startTime   time.Time
	lastWrite   time.Time

	listeners           map[string]listeners.Listener
	listenerDiffsCached map[string]listeners.Diff
}

func New(opts *Opts) (*Mon, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("failed to configure mon: %w", err)
	}

	fileMonitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:    opts.ProjectDir,
		WatchRoot:   true,
		TrackWrites: true,
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

		listeners:           map[string]listeners.Listener{},
		listenerDiffsCached: listeners.DiffMap{},
	}

	if err := mon.setupListeners(); err != nil {
		return nil, fmt.Errorf("failed to set up listeners: %w", err)
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

	snapshot := m.GetStatusSnapshot(true, true)
	fmt.Println(clearLine + snapshot.Final())

	return nil
}

func (m *Mon) Teardown() {
	close(m.displayChan)
}

func (m *Mon) setupListeners() error {
	fileMap := m.fileMonitor.FileMap()

	for _, listener := range m.Listeners {
		for _, file := range listener.WatchedFiles() {
			m.listeners[file] = listener

			initialFiles := fileMap.FilePathsByBase(file)
			for _, path := range initialFiles {
				slog.Debug("found file for listener", "listener", listener.Name(), "path", path)

				content, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("failed to read file %q for listener %q: %w", path, listener.Name(), err)
				}

				logErr := listener.LogEvent(listeners.Event{
					Name:    path,
					Type:    listeners.EventInit,
					Content: content,
				})
				if logErr != nil {
					return fmt.Errorf("failed to log initializing event for file %q for listener %q: %w", path, listener.Name(), logErr)
				}
			}
		}
	}

	return nil
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

			go m.handleFileEvent(ctx, event)

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

		base := filepath.Base(event.Name)
		for file, listener := range m.listeners {
			if base == file {
				content, err := os.ReadFile(event.Name)
				if err != nil {
					slog.Error("failed to read contents of file for listener", "name", event.Name, "error", err, "listener", listener.Name())
					continue
				}

				logErr := listener.LogEvent(listeners.Event{
					Name:    event.Name,
					Type:    listeners.EventWrite,
					Content: content,
				})
				if logErr != nil {
					slog.Error("failed to log event for listener", "listener", listener.Name(), "error", logErr)
				}

				slog.Debug("logged update to listened file", "listener", listener.Name(), "path", event.Name)
			}
		}
	}
}
