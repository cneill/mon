package git

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/cneill/mon/pkg/files"
	"github.com/go-git/go-git/v5"
)

type MonitorOpts struct {
	RootPath string
}

func (m *MonitorOpts) OK() error {
	if m.RootPath == "" {
		return fmt.Errorf("must supply root path")
	}

	return nil
}

type Monitor struct {
	FileEvents chan files.Event
	GitEvents  chan Event

	gitLogPath  string
	fileMonitor *files.Monitor
	repo        *git.Repository

	mutex             sync.RWMutex
	initialHash       string
	lastProcessedHash string
	numCommits        int64
	linesAdded        int64
	linesDeleted      int64
	unstagedChanges   int64
	gitFiles          map[string]struct{}
}

func NewMonitor(opts *MonitorOpts) (*Monitor, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("invalid git monitor options: %w", err)
	}

	repo, err := OpenGitRepo(opts.RootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repo in project dir %q: %w", opts.RootPath, err)
	}

	initialHash, err := GetHEADSHA(repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get initial git HEAD SHA: %w", err)
	}

	gitLogPath, err := filepath.Abs(filepath.Join(opts.RootPath, ".git", "logs", "HEAD"))
	if err != nil {
		return nil, fmt.Errorf("failed to get git log path: %w", err)
	}

	if _, err := os.Stat(gitLogPath); err != nil {
		return nil, fmt.Errorf("git logs not found at %s", gitLogPath)
	}

	fm, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  opts.RootPath,
		WatchRoot: false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set up file monitor to watch git log: %w", err)
	}

	if err := fm.WatchFile(gitLogPath); err != nil {
		return nil, fmt.Errorf("failed to set up monitoring for git log file: %w", err)
	}

	monitor := &Monitor{
		FileEvents: make(chan files.Event, 10),
		GitEvents:  make(chan Event, 10),

		gitLogPath:  gitLogPath,
		fileMonitor: fm,
		repo:        repo,

		initialHash: initialHash,
		gitFiles:    map[string]struct{}{},
	}

	if err := monitor.updateTrackedFiles(); err != nil {
		return nil, fmt.Errorf("failed to populate initial git files: %w", err)
	}

	go monitor.Update(context.Background())

	return monitor, nil
}

func (m *Monitor) Run(ctx context.Context) {
	go m.fileMonitor.Run(ctx)

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-m.fileMonitor.Events:
			if !ok {
				slog.Info("git log events channel closed")
				return
			}

			switch event.Type() { //nolint:exhaustive
			case files.EventTypeChmod, files.EventTypeWrite:
				slog.Debug("Updating due to git log update", "event", event)

				go m.Update(ctx)

				if err := m.updateTrackedFiles(); err != nil {
					slog.Error("failed to update list of tracked files after git log update")
				}
			}

		case event, ok := <-m.FileEvents:
			if !ok {
				slog.Info("file events channel closed")
				return
			}

			m.mutex.RLock()

			if _, tracked := m.gitFiles[event.Name]; !tracked {
				m.mutex.RUnlock()
				continue
			}

			m.mutex.RUnlock()

			slog.Debug("Updating due to file event", "event", event)

			go m.Update(ctx)
		}
	}
}

func (m *Monitor) Update(ctx context.Context) {
	slog.Debug("Processing git change")

	commits, err := CommitsSince(m.repo, m.initialHash)
	if err != nil {
		slog.Error("failed to list commits since initialization", "error", err)
		return
	}

	m.mutex.Lock()
	m.numCommits = int64(len(commits))
	m.mutex.Unlock()

	newHash, err := GetHEADSHA(m.repo)
	if err != nil {
		slog.Error("failed to get new git SHA", "error", err)
		return
	}

	patch, err := PatchSince(m.repo, m.initialHash)
	if err != nil {
		slog.Error("failed to generate patch", "initial_hash", m.initialHash, "head_hash", newHash)
		return
	}

	adds, deletes := PatchAddsDeletes(patch)
	m.linesAdded = adds
	m.linesDeleted = deletes

	unstagedCount, err := UnstagedChangeCount(m.repo)
	if err != nil {
		slog.Error("failed to check unstaged changes", "error", err)
		return
	}

	m.unstagedChanges = unstagedCount

	m.lastProcessedHash = newHash

	event := Event{
		Type: EventTypeNewCommit,
	}

	select {
	case <-ctx.Done():
		return
	case m.GitEvents <- event:
	}
}

func (m *Monitor) Close() {
	// close(m.FileEvents)
	close(m.GitEvents)
	m.fileMonitor.Close()
}

func (m *Monitor) updateTrackedFiles() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.gitFiles = map[string]struct{}{}

	initialFiles, err := ListFiles(m.repo)
	if err != nil {
		return fmt.Errorf("failed to list initial files: %w", err)
	}

	for _, file := range initialFiles {
		m.gitFiles[file] = struct{}{}
	}

	return nil
}
