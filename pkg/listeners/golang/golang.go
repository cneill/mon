package golang

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cneill/mon/pkg/deps"
	"github.com/cneill/mon/pkg/listeners"

	"golang.org/x/mod/modfile"
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

func (l *Listener) LogEvent(event listeners.Event) error {
	base := filepath.Base(event.Name)

	switch event.Type {
	case listeners.EventInit:
		if base == "go.mod" {
			slog.Debug("got write event for go.mod file", "path", event.Name)
			l.mutex.Lock()
			l.modFiles = append(l.modFiles, &ModFile{
				Path:           event.Name,
				InitialContent: event.Content,
			})
			l.mutex.Unlock()
		}

	case listeners.EventWrite:
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
	builder := &strings.Builder{}

	for _, modFile := range l.modFiles {
		diff := modFile.Diff()
		if diff != "" {
			builder.WriteString(modFile.Path + ":\n")
			builder.WriteString(diff + "\n\n")
		}
	}

	return builder.String()
}

type ModFile struct {
	Path           string
	InitialContent []byte
	LatestContent  []byte
}

func (m *ModFile) Diff() string {
	if m.LatestContent == nil {
		return ""
	}

	initialDeps, err := ParseDeps(m.Path, m.InitialContent)
	if err != nil {
		slog.Error("initial go.mod file invalid", "error", err)
		return ""
	}

	latestDeps, err := ParseDeps(m.Path, m.LatestContent)
	if err != nil {
		slog.Error("current go.mod file invalid", "error", err)
		return ""
	}

	return latestDeps.Diff(initialDeps)
}

func ParseDeps(modFilePath string, modFileContents []byte) (deps.Dependencies, error) {
	parsedFile, err := modfile.Parse(modFilePath, modFileContents, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod file %q: %w", modFilePath, err)
	}

	results := make(deps.Dependencies, len(parsedFile.Require))
	for i, require := range parsedFile.Require {
		results[i] = deps.Dependency{
			Name:    "",
			URL:     require.Mod.Path,
			Version: require.Mod.Version,
		}
	}

	return results, nil
}
