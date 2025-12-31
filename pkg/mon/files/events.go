package files

import (
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

func (f *FileMonitor) handleCreate(event Event) error {
	f.pendingDeleteMutex.Lock()

	if _, ok := f.pendingDeletes[event.Name]; ok {
		delete(f.pendingDeletes, event.Name)
		slog.Debug("ignored delete+create pair", "name", event.Name)

		return nil
	}

	f.pendingDeleteMutex.Unlock()

	if _, err := f.fileMap.Get(event.Name); !errors.Is(err, ErrUnknownFile) {
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

	if err := f.fileMap.AddFile(event.Name, info); err != nil {
		return fmt.Errorf("failed to add new file %q upon creation event: %w", event.Name, err)
	}

	if fi.IsDir() {
		if err := f.WatchDirRecursive(event.Name); err != nil {
			return err
		}
	}

	f.Events <- event

	return nil
}

func (f *FileMonitor) handleRemoveOrRename(event Event) error {
	file, err := f.fileMap.Get(event.Name)
	if err != nil {
		return fmt.Errorf("got remove/rename event for unknown file %q", event.Name)
	}

	pd := pendingDelete{
		timestamp:   time.Now(),
		event:       event,
		initialFile: file.IsInitial(),
	}

	slog.Debug("pending delete", "name", event.Name, "type", file.FileType)

	f.pendingDeleteMutex.Lock()
	f.pendingDeletes[event.Name] = pd
	f.pendingDeleteMutex.Unlock()

	return nil
}
