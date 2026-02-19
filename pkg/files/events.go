package files

import (
	"context"
	"fmt"
	"log/slog"
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
	case e.Op.Has(fsnotify.Create):
		return EventTypeCreate
	case e.Op.Has(fsnotify.Remove):
		return EventTypeRemove
	case e.Op.Has(fsnotify.Rename):
		return EventTypeRename
	case e.Op.Has(fsnotify.Write):
		return EventTypeWrite
	case e.Op.Has(fsnotify.Chmod):
		return EventTypeChmod
	}

	return EventTypeUnknown
}

func (m *Monitor) handleCreate(ctx context.Context, event Event) error {
	m.pendingDeleteMutex.Lock()

	if _, ok := m.pendingDeletes[event.Name]; ok {
		delete(m.pendingDeletes, event.Name)
		m.pendingDeleteMutex.Unlock()

		// Editor swap detected - count this as a write to the file
		if m.opts.TrackWrites {
			if err := m.fileMap.AddSwapWrite(event.Name); err != nil {
				slog.Error("failed to record swap write", "name", event.Name, "error", err)
			}

			m.pushEvent(ctx, Event{
				Name: event.Name,
				Op:   fsnotify.Write,
			})
		}

		slog.Debug("detected editor swap, counted as write", "name", event.Name)

		return nil
	}

	m.pendingDeleteMutex.Unlock()

	if m.fileMap.Has(event.Name) {
		slog.Debug("got duplicate creation request, ignoring", "name", event.Name)
		return nil
	}

	if err := m.fileMap.AddNewPath(event.Name); err != nil {
		return err
	}

	slog.Debug("Added new file after creation event", "name", event.Name)

	if m.fileMap.IsDir(event.Name) {
		go func() {
			// We want to try to catch e.g. mkdir -p calls that rapidly create nested directories
			time.Sleep(time.Millisecond * 250)

			if err := m.WatchDirRecursive(event.Name, false); err != nil {
				slog.Error("failed to monitor new directory", "path", event.Name, "error", err)
				// return err
			}
		}()
	}

	m.pushEvent(ctx, event)

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

	// Mark file as potentially being swapped - this prevents counting writes
	// that happen between the delete and create events of an editor swap
	m.fileMap.MarkPendingSwap(event.Name)

	m.pendingDeleteMutex.Lock()
	m.pendingDeletes[event.Name] = pd
	m.pendingDeleteMutex.Unlock()

	return nil
}
