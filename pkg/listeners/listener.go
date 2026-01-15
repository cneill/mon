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

type DiffMap map[string]Diff

func (d DiffMap) IsEmpty() bool {
	for _, diff := range d {
		if !diff.IsEmpty() {
			return false
		}
	}

	return true
}

func (d DiffMap) NumNewDependencies() int64 {
	var result int64

	for _, diff := range d {
		result += diff.DependencyFileDiffs.NumNewDependencies()
	}

	return result
}

func (d DiffMap) NumDeletedDependencies() int64 {
	var result int64

	for _, diff := range d {
		result += diff.DependencyFileDiffs.NumDeletedDependencies()
	}

	return result
}

func (d DiffMap) NumUpdatedDependencies() int64 {
	var result int64

	for _, diff := range d {
		result += diff.DependencyFileDiffs.NumUpdatedDependencies()
	}

	return result
}
