package files

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
)

type EventType string

const (
	EventTypeUnknown EventType = "unknown"
	EventTypeChmod   EventType = "chmod"
	EventTypeCreate  EventType = "create"
	EventTypeRemove  EventType = "remove"
	EventTypeRename  EventType = "rename"
	EventTypeWrite   EventType = "write"
)

type Event struct {
	Name string
	Op   fsnotify.Op
}

func (e Event) Type() EventType {
	switch {
	case e.Op.Has(fsnotify.Chmod):
		return EventTypeChmod
	case e.Op.Has(fsnotify.Create):
		return EventTypeCreate
	case e.Op.Has(fsnotify.Remove):
		return EventTypeRemove
	case e.Op.Has(fsnotify.Rename):
		return EventTypeRename
	case e.Op.Has(fsnotify.Write):
		return EventTypeWrite
	}

	return EventTypeUnknown
}

func (m *Monitor) handleCreate(ctx context.Context, event Event) error {
	m.pendingDeleteMutex.Lock()

	if _, ok := m.pendingDeletes[event.Name]; ok {
		delete(m.pendingDeletes, event.Name)
		slog.Debug("ignored delete+create pair", "name", event.Name)

		return nil
	}

	m.pendingDeleteMutex.Unlock()

	if _, err := m.fileMap.Get(event.Name); !errors.Is(err, ErrUnknownFile) {
		return fmt.Errorf("creation request for file %q that already exists", event.Name)
	}

	fi, err := os.Stat(event.Name)
	if err != nil {
		return fmt.Errorf("failed to stat new file %q: %w", event.Name, err)
	}

	info := FileInfo{
		FileInfo: fi,
		FileType: FileTypeNew,
	}

	if err := m.fileMap.AddFile(event.Name, info); err != nil {
		return fmt.Errorf("failed to add new file %q upon creation event: %w", event.Name, err)
	}

	slog.Debug("Added new file", "name", event.Name)

	if fi.IsDir() {
		// We want to try to catch e.g. mkdir -p calls that rapidly create nested directories
		time.Sleep(time.Millisecond * 250)

		if err := m.WatchDirRecursive(event.Name, false); err != nil {
			return err
		}
	}

	select {
	case <-ctx.Done():
		return nil
		// return fmt.Errorf("context cancelled")
	case m.Events <- event:
	}

	return nil
}

func (m *Monitor) handleRemoveOrRename(_ context.Context, event Event) error {
	file, err := m.fileMap.Get(event.Name)
	if err != nil {
		return fmt.Errorf("got remove/rename event for unknown file %q", event.Name)
	}

	pd := pendingDelete{
		timestamp:   time.Now(),
		event:       event,
		initialFile: file.IsInitial(),
	}

	slog.Debug("pending delete", "name", event.Name, "type", file.FileType)

	m.pendingDeleteMutex.Lock()
	m.pendingDeletes[event.Name] = pd
	m.pendingDeleteMutex.Unlock()

	return nil
}
