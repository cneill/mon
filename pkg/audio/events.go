package audio

import (
	"context"
	"log/slog"
	"slices"
	"time"
)

type EventType string

const (
	EventInit            EventType = "init"
	EventGitCommitCreate EventType = "git_commit_create"
	EventGitCommitPush   EventType = "git_commit_push"
	EventFileCreate      EventType = "file_create"
	EventFileWrite       EventType = "file_write"
	EventFileRemove      EventType = "file_remove"
	EventPackageCreate   EventType = "package_create"
	EventPackageUpgrade  EventType = "package_upgrade"
	EventPackageRemove   EventType = "package_remove"
)

func ValidEventType(eventType EventType) bool {
	return slices.Contains([]EventType{
		EventInit, EventGitCommitCreate, EventGitCommitPush, EventFileCreate, EventFileWrite, EventFileRemove,
		EventPackageCreate, EventPackageUpgrade, EventPackageRemove,
	}, eventType)
}

type Event struct {
	Type EventType
	Time time.Time
}

func (m *Manager) SendEvent(ctx context.Context, event Event) {
	if !m.limiter.Allow() {
		return
	}

	select {
	case <-ctx.Done():
		return
	case m.eventChan <- event:
		slog.Debug("sent sound event", "event", event)
	}
}

func (m *Manager) eventLoop(ctx context.Context) {
	for event := range m.eventChan {
		select {
		case <-ctx.Done():
			return
		default:
		}

		soundName, ok := m.hookMap[event.Type]
		if !ok {
			return
		}

		go func() {
			if err := m.PlaySound(ctx, soundName); err != nil {
				slog.Error("Failed to play sound", "name", soundName, "error", err)
			}
		}()
	}
}
