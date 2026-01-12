package listeners

import "github.com/cneill/mon/pkg/deps"

type Listener interface {
	Name() string
	WatchedFiles() []string
	LogEvent(event Event) error
	Diff() Diff
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

type Diff struct {
	DependencyFileDiffs deps.FileDiffs
}

func (d Diff) IsEmpty() bool {
	return d.DependencyFileDiffs.AllEmpty()
}
