package git

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

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

	gitLogPath       string
	gitRemoteLogPath string
	fileMonitor      *files.Monitor
	repo             *git.Repository

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

	currentBranch, err := CurrentBranch(repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get current branch: %w", err)
	}

	gitRemoteLogPath, err := filepath.Abs(filepath.Join(opts.RootPath, ".git", "logs", "refs", "remotes", git.DefaultRemoteName, currentBranch.Short()))
	if err != nil {
		return nil, fmt.Errorf("failed to get path to remote git log: %w", err)
	}

	if _, err := os.Stat(gitRemoteLogPath); err != nil {
		return nil, fmt.Errorf("git remote logs not found at %s", gitRemoteLogPath)
	}

	fm, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:    opts.RootPath,
		WatchRoot:   false,
		TrackWrites: false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set up file monitor to watch git log: %w", err)
	}

	if err := fm.WatchFile(gitLogPath, true); err != nil {
		return nil, fmt.Errorf("failed to set up monitoring for git log file: %w", err)
	}

	if err := fm.WatchFile(gitRemoteLogPath, true); err != nil {
		return nil, fmt.Errorf("failed to set up monitoring for git remote log file: %w", err)
	}

	monitor := &Monitor{
		FileEvents: make(chan files.Event, 10),
		GitEvents:  make(chan Event, 10),

		gitLogPath:       gitLogPath,
		gitRemoteLogPath: gitRemoteLogPath,
		fileMonitor:      fm,
		repo:             repo,

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

		// m.fileMonitor tracks file events on the git log file
		case event, ok := <-m.fileMonitor.Events:
			if !ok {
				slog.Info("git log events channel closed")
				return
			}

			if event.Type() == files.EventTypeWrite {
				switch event.Name {
				case m.gitLogPath:
					slog.Debug("Updating due to git log update", "event", event)

					go m.Update(ctx)

					if err := m.updateTrackedFiles(); err != nil {
						slog.Error("failed to update list of tracked files after git log update")
					}
				case m.gitRemoteLogPath:
					slog.Debug("Got remote update, checking for push...")

					contents, err := os.ReadFile(m.gitRemoteLogPath)
					if err != nil {
						slog.Error("failed to read git remote log file", "error", err)
					}

					lines := bytes.Split(contents, []byte("\n"))
					if bytes.Contains(lines[len(lines)-1], []byte("update by push")) { // default for push in reflog
						go m.pushEvent(ctx, EventTypeCommitPush)
					}
				}
			}

		// FileEvents come in from the broader file monitor, we use them to update the lines modified/etc stats for
		// git-tracked files, if they are tracked by git
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

// Update tracks the number of commits since the original commit, as well as changes to git-tracked files.
func (m *Monitor) Update(ctx context.Context) {
	slog.Debug("Updating git status")

	m.mutex.Lock()
	defer m.mutex.Unlock()

	commits, err := CommitsSince(m.repo, m.initialHash)
	if err != nil {
		slog.Error("failed to list commits since initialization", "error", err)
		return
	}

	updatedNumCommits := int64(len(commits))

	if updatedNumCommits != m.numCommits {
		go m.pushEvent(ctx, EventTypeNewCommit)
	}

	m.numCommits = updatedNumCommits

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
}

func (m *Monitor) Close() {
	// close(m.FileEvents)
	close(m.GitEvents)
	m.fileMonitor.Close()
}

func (m *Monitor) pushEvent(ctx context.Context, eventType EventType) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	gitEvent := Event{
		Time: time.Now(),
		Type: eventType,
	}

	select {
	case <-ctx.Done():
		if err := ctx.Err(); err != nil {
			slog.Error("context error pushing event from git monitor", "error", err)
		}

		return
	case m.GitEvents <- gitEvent:
	}
}

func (m *Monitor) updateTrackedFiles() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.gitFiles = map[string]struct{}{}

	currentFiles, err := ListFiles(m.repo)
	if err != nil {
		return fmt.Errorf("failed to list git-tracked files: %w", err)
	}

	for _, file := range currentFiles {
		m.gitFiles[file] = struct{}{}
	}

	return nil
}
