package files

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type MonitorOpts struct {
	RootPath  string
	WatchRoot bool
}

func (m *MonitorOpts) OK() error {
	if m.RootPath == "" {
		return fmt.Errorf("must supply root path")
	}

	return nil
}

type Monitor struct {
	Events chan Event

	rootPath string

	watcher *fsnotify.Watcher
	fileMap *FileMap

	pendingDeletes     map[string]pendingDelete // key: name
	pendingDeleteMutex sync.RWMutex
	deleteTimeout      time.Duration
}

func NewMonitor(opts *MonitorOpts) (*Monitor, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("invalid file monitor options: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize fsnotify watcher: %w", err)
	}

	monitor := &Monitor{
		Events: make(chan Event),

		rootPath: opts.RootPath,

		watcher: watcher,
		fileMap: NewFileMap(),

		pendingDeletes: map[string]pendingDelete{},
		deleteTimeout:  time.Millisecond * 250,
	}

	if err := monitor.populateInitialFiles(); err != nil {
		return nil, err
	}

	if opts.WatchRoot {
		if err := monitor.WatchDirRecursive(opts.RootPath, true); err != nil {
			return nil, err
		}
	}

	return monitor, nil
}

func (m *Monitor) WatchDirRecursive(path string, initial bool) error {
	err := filepath.WalkDir(path, func(walkPath string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		if filepath.Base(walkPath) == ".git" {
			return filepath.SkipDir
		}

		if err := m.watcher.Add(walkPath); err != nil {
			return fmt.Errorf("failed to monitor directory %q: %w", walkPath, err)
		}

		if !m.fileMap.Has(walkPath) && !initial {
			stat, err := os.Stat(walkPath)
			if err != nil {
				slog.Error("failed to stat file while walking to recursively watch", "root_path", path, "walk_path", walkPath, "error", err)
			}

			info := FileInfo{
				FileInfo: stat,
				FileType: FileTypeNew,
			}

			if err := m.fileMap.AddFile(walkPath, info); err != nil {
				return fmt.Errorf("failed to watch discovered directory: %w", err)
			}

			slog.Debug("Added new directory", "path", walkPath)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to set up recursive directory watching for %q: %w", path, err)
	}

	watched := m.watcher.WatchList()
	slog.Debug("Watch list", "paths", watched)

	return nil
}

func (m *Monitor) WatchFile(path string) error {
	if err := m.watcher.Add(path); err != nil {
		return fmt.Errorf("failed to monitor file %q: %w", path, err)
	}

	return nil
}

func (m *Monitor) Run(ctx context.Context) {
	go m.processPendingDeletes(ctx)

	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err != nil {
				slog.Error("context error", "error", err)
			}

			return
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}

			wrapped := Event{
				Name: event.Name,
				Op:   event.Op,
			}

			if m.ignoreEvent(wrapped) {
				continue
			}

			switch wrapped.Type() {
			case EventTypeCreate:
				if err := m.handleCreate(wrapped); err != nil {
					slog.Error("failed to handle create event", "name", wrapped.Name, "error", err)
				}
			case EventTypeRemove, EventTypeRename:
				if err := m.handleRemoveOrRename(wrapped); err != nil {
					slog.Error("failed to handle remove or rename event", "name", wrapped.Name, "error", err)
				}
			case EventTypeWrite, EventTypeChmod, EventTypeUnknown:
				m.Events <- wrapped
			}

		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}

			slog.Error("watcher error", "error", err)
		}
	}
}

func (m *Monitor) Close() {
	close(m.Events)

	if err := m.watcher.Close(); err != nil {
		slog.Error("Failed to shut down fsnotify watcher", "error", err)
	}
}

func (m *Monitor) ignoreEvent(event Event) bool {
	// Ignore VIM temp files: backups (~, .swp), swap (numeric names)
	base := filepath.Base(event.Name)
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") || isNumeric(base) {
		slog.Debug("ignoring editor file swaps")
		return true
	}

	return false
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

func (m *Monitor) populateInitialFiles() error {
	// Scan initial files (non-dirs, skip .git)
	scanErr := filepath.WalkDir(m.rootPath, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if de.IsDir() && filepath.Base(path) == ".git" {
			return filepath.SkipDir
		}

		info, err := de.Info()
		if err != nil {
			slog.Error("failed to get file info for file", "path", path, "error", err)
			return nil
		}

		fi := FileInfo{
			FileInfo: info,
			FileType: FileTypeInitial,
		}

		if err := m.fileMap.AddFile(path, fi); err != nil {
			return fmt.Errorf("failed to add file %q to map: %w", path, err)
		}

		return nil
	})
	if scanErr != nil {
		return fmt.Errorf("failed to scan initial files: %w", scanErr)
	}

	return nil
}

type pendingDelete struct {
	timestamp   time.Time
	event       Event
	initialFile bool
}

func (m *Monitor) processPendingDeletes(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pendingDeleteMutex.Lock()

			for fileName, pd := range m.pendingDeletes {
				if time.Since(pd.timestamp) < m.deleteTimeout {
					continue
				}

				delete(m.pendingDeletes, fileName)

				info, err := m.fileMap.Get(fileName)
				if err != nil {
					slog.Error("failed to get file for deletion", "name", fileName, "error", err)
					continue
				}

				if err := m.fileMap.Delete(fileName); err != nil {
					slog.Error("failed to process pending deletion event", "name", fileName, "error", err)
					continue
				}

				slog.Debug("confirmed delete", "name", fileName, "type", info.FileType)

				m.Events <- pd.event
			}

			m.pendingDeleteMutex.Unlock()
		}
	}
}
