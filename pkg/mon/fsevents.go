package mon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

type EventType string

const (
	EventTypeUnknown EventType = "unknown"
	EventTypeCreate  EventType = "create"
	EventTypeRemove  EventType = "remove"
	EventTypeRename  EventType = "rename"
	EventTypeWrite   EventType = "write"
)

func getEventType(event fsnotify.Event) EventType {
	switch {
	case event.Op&fsnotify.Create != 0:
		return EventTypeCreate
	case event.Op&fsnotify.Remove != 0:
		return EventTypeRemove
	case event.Op&fsnotify.Rename != 0:
		return EventTypeRename
	case event.Op&fsnotify.Write != 0:
		return EventTypeWrite
	}

	return EventTypeUnknown
}

func ignoreEvent(event fsnotify.Event) bool {
	if strings.HasPrefix(event.Name, ".git") {
		return true
	}

	// Ignore editor temp files: backups (~, .swp), swap (numeric names)
	base := filepath.Base(event.Name)
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") || isNumeric(base) {
		return true
	}

	return false
}

func (m *Mon) handleEvents() {
	if m.watcher == nil {
		panic(fmt.Errorf("watcher wasn't set up first to monitor events"))
	}

	for {
		select {
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}

			if ignoreEvent(event) {
				continue
			}

			eventType := getEventType(event)

			if event.Name == m.gitLogPath && (event.Op&(fsnotify.Write|fsnotify.Chmod) != 0) {
				go m.processGitChange()
				continue
			}

			switch eventType {
			case EventTypeCreate:
				if err := m.handleCreate(event); err != nil {
					slog.Error("failed to handle create event", "error", err)
				}

			case EventTypeRemove, EventTypeRename:
				if err := m.handleRemoveOrRename(event); err != nil {
					slog.Error("failed to handle "+string(eventType)+" event: %w", "error", err)
				}

			case EventTypeWrite:
				time.Sleep(time.Millisecond * 250) // allow write+delete pairs to settle before checking

				if m.writeLimiter.Allow() {
					m.writeLimiter.Reserve()

					go m.processGitChange()
				}
			}

		case err, ok := <-m.watcher.Errors:
			if ok {
				slog.Error("watcher error", "error", err)
			}
		}
	}
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

func (m *Mon) handleCreate(event fsnotify.Event) error {
	// Check for matching pending delete
	if pdI, pending := m.pendingDeletes.LoadAndDelete(event.Name); pending {
		pd := pdI.(pendingDelete)
		if pd.wasNewFile {
			// Restore to newFiles, no count change needed
			m.newFiles.Store(event.Name, struct{}{})
		} else {
			// Restore to initialFiles, no count change needed
			m.initialFiles.Store(event.Name, struct{}{})
		}

		slog.Debug("ignored delete+create pair", "name", event.Name)

		return nil
	}

	// Only count if not already tracked in either map
	_, inInitial := m.initialFiles.Load(event.Name)
	_, inNew := m.newFiles.Load(event.Name)

	if !inInitial && !inNew {
		slog.Debug("added file", "name", event.Name)
		m.filesCreated.Add(1)
		m.newFiles.Store(event.Name, struct{}{})
		m.triggerDisplay()
	}

	if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
		m.addRecursiveWatchesForDir(event.Name)
	}

	return nil
}

func (m *Mon) handleRemoveOrRename(event fsnotify.Event) error {
	// Check if in newFiles first
	if _, exists := m.newFiles.LoadAndDelete(event.Name); exists {
		slog.Debug("pending delete (new file)", "name", event.Name)
		m.pendingDeletes.Store(event.Name, pendingDelete{
			timestamp:  time.Now(),
			wasNewFile: true,
		})

		return nil
	}

	// Check if in initialFiles
	if _, exists := m.initialFiles.LoadAndDelete(event.Name); exists {
		slog.Debug("pending delete (initial file)", "name", event.Name)
		m.pendingDeletes.Store(event.Name, pendingDelete{
			timestamp:  time.Now(),
			wasNewFile: false,
		})
	}

	return nil
}
