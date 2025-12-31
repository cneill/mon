package files

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type FileMonitorOpts struct {
	RootPath string
}

func (o *FileMonitorOpts) OK() error {
	if o.RootPath == "" {
		return fmt.Errorf("must supply root path")
	}

	return nil
}

type FileMonitor struct {
	Events chan Event

	rootPath string

	watcher *fsnotify.Watcher
	fileMap *FileMap

	pendingDeletes     map[string]pendingDelete // key: name
	pendingDeleteMutex sync.RWMutex
	deleteTimeout      time.Duration
}

func NewFileMonitor(opts *FileMonitorOpts) (*FileMonitor, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("invalid FileMon options: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize fsnotify watcher: %w", err)
	}

	monitor := &FileMonitor{
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

	if err := monitor.WatchDirRecursive(opts.RootPath); err != nil {
		return nil, err
	}

	return monitor, nil
}

func (f *FileMonitor) WatchDirRecursive(path string) error {
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

		if err := f.watcher.Add(walkPath); err != nil {
			return fmt.Errorf("failed to monitor directory %q: %w", walkPath, err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to set up recursive directory watching for %q: %w", path, err)
	}

	watched := f.watcher.WatchList()
	slog.Debug("Watch list", "paths", watched)

	return nil
}

func (f *FileMonitor) WatchFile(path string) error {
	if err := f.watcher.Add(path); err != nil {
		return fmt.Errorf("failed to monitor file %q: %w", path, err)
	}

	return nil
}

func (f *FileMonitor) Run(ctx context.Context) {
	defer f.Close()

	go f.processPendingDeletes(ctx)

	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err != nil {
				slog.Error("context error", "error", err)
			}

			return
		case event, ok := <-f.watcher.Events:
			if !ok {
				return
			}

			wrapped := Event{
				Name: event.Name,
				Op:   event.Op,
			}

			switch wrapped.Type() {
			case EventTypeCreate:
				if err := f.handleCreate(wrapped); err != nil {
					slog.Error("failed to handle create event", "name", wrapped.Name, "error", err)
				}
			case EventTypeRemove, EventTypeRename:
				if err := f.handleRemoveOrRename(wrapped); err != nil {
					slog.Error("failed to handle remove or rename event", "name", wrapped.Name, "error", err)
				}
			case EventTypeWrite, EventTypeChmod, EventTypeUnknown:
				// TODO: moar?
				f.Events <- wrapped
			}

		case err, ok := <-f.watcher.Errors:
			if !ok {
				return
			}

			slog.Error("watcher error", "error", err)
		}
	}
}

func (f *FileMonitor) Close() {
	close(f.Events)

	if err := f.watcher.Close(); err != nil {
		slog.Error("Failed to shut down fsnotify watcher", "error", err)
	}
}

func (f *FileMonitor) populateInitialFiles() error {
	// Scan initial files (non-dirs, skip .git)
	scanErr := filepath.WalkDir(f.rootPath, func(path string, de os.DirEntry, err error) error {
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

		if err := f.fileMap.AddFile(path, fi); err != nil {
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

func (f *FileMonitor) processPendingDeletes(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.pendingDeleteMutex.Lock()

			for fileName, pd := range f.pendingDeletes {
				if time.Since(pd.timestamp) > f.deleteTimeout {
					delete(f.pendingDeletes, fileName)

					info, err := f.fileMap.Get(fileName)
					if err != nil {
						slog.Error("failed to get file for deletion", "name", fileName, "error", err)
						continue
					}

					if err := f.fileMap.Delete(fileName); err != nil {
						slog.Error("failed to process pending deletion event", "name", fileName, "error", err)
						continue
					}

					slog.Debug("confirmed delete", "name", fileName, "type", info.FileType)

					f.Events <- pd.event
				}
			}

			f.pendingDeleteMutex.Unlock()
		}
	}
}
