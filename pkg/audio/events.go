package audio

import (
	"context"
	"log/slog"
	"slices"
	"time"
)

type EventType string

const (
	EventCommitNew      EventType = "commit_new"
	EventFileNew        EventType = "file_new"
	EventFileDelete     EventType = "file_delete"
	EventPackageNew     EventType = "package_new"
	EventPackageUpgrade EventType = "package_upgrade"
	EventPackageDelete  EventType = "package_delete"
)

func ValidEventType(eventType EventType) bool {
	return slices.Contains([]EventType{
		EventCommitNew, EventFileNew, EventFileDelete, EventPackageNew, EventPackageUpgrade, EventPackageDelete,
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
