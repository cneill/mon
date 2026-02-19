package audio

import (
	"context"
	"log/slog"
	"slices"
	"time"
)

type EventType string

const (
	EventInit           EventType = "init"
	EventCommitCreate   EventType = "commit_create"
	EventFileCreate     EventType = "file_create"
	EventFileWrite      EventType = "file_write"
	EventFileRemove     EventType = "file_remove"
	EventPackageCreate  EventType = "package_create"
	EventPackageUpgrade EventType = "package_upgrade"
	EventPackageRemove  EventType = "package_remove"
)

func ValidEventType(eventType EventType) bool {
	return slices.Contains([]EventType{
		EventInit, EventCommitCreate, EventFileCreate, EventFileWrite, EventFileRemove,
		EventPackageCreate, EventPackageUpgrade, EventPackageRemove,
	}, eventType)
}

type Event struct {
	Type EventType
	Time time.Time
}

func (m *Manager) SendEvent(ctx context.Context, e Event) {
	m.hookMutex.RLock()
	defer m.hookMutex.RUnlock()

	soundName, ok := m.hookMap[e.Type]
	if !ok {
		return
	}

	go func() {
		if err := m.PlaySound(ctx, soundName); err != nil {
			slog.Error("Failed to play sound", "name", soundName, "error", err)
		}
	}()
}
