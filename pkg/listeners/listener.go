package listeners

type Listener interface {
	Name() string
	WatchedFiles() []string
	LogEvent(event Event) error
	Diff() string
}

type EventLogger func(event Event) error

type Event struct {
	Name    string
	Type    EventType
	Content []byte
}

type EventType string

const (
	EventInit  EventType = "init"
	EventWrite EventType = "write"
)
