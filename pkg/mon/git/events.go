package git

type EventType string

const (
	EventTypeUnknown   EventType = "unknown"
	EventTypeNewCommit EventType = "new commit"
)

type Event struct {
	Type EventType
}
