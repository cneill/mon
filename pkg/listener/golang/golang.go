package golang

import (
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/cneill/mon/pkg/listener"
)

type Listener struct {
	mutex    sync.RWMutex
	modFiles []*ModFile
}

func New() *Listener {
	return &Listener{
		modFiles: []*ModFile{},
	}
}

func (l *Listener) Name() string { return "golang" }

func (l *Listener) WatchedFiles() []string {
	return []string{
		"go.mod",
	}
}

func (l *Listener) LogEvent(event listener.Event) error {
	base := filepath.Base(event.Name)

	switch event.Type {
	case listener.EventInit:
		if base == "go.mod" {
			slog.Debug("got write event for go.mod file", "path", event.Name)
			l.mutex.Lock()
			l.modFiles = append(l.modFiles, &ModFile{
				Path:           event.Name,
				InitialContent: event.Content,
			})
			l.mutex.Unlock()
		}

	case listener.EventWrite:
		if base == "go.mod" {
			for _, modFile := range l.modFiles {
				if modFile.Path == event.Name {
					l.mutex.Lock()
					slog.Debug("got write event for go.mod file", "path", event.Name)
					l.mutex.Unlock()

					modFile.LatestContent = event.Content
				}
			}
		}
	}

	return nil
}

func (l *Listener) Diff() string {
	return ""
}

type ModFile struct {
	Path           string
	InitialContent []byte
	LatestContent  []byte
}
