package git

import "time"

type EventType string

const (
	EventTypeUnknown   EventType = "unknown"
	EventTypeNewCommit EventType = "new commit"
	EventTypePush      EventType = "push"
)

type Event struct {
	Time time.Time
	Type EventType
}
