package golang

import (
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cneill/mon/pkg/listeners"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
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

func (m *ModFile) Diff() string { //nolint:cyclop
	if m.LatestContent == nil {
		return ""
	}

	initialFile, err := modfile.Parse(m.Path, m.InitialContent, nil)
	if err != nil {
		slog.Error("failed to parse initial contents of go.mod file", "path", m.Path, "error", err)
		return ""
	}

	latestFile, err := modfile.Parse(m.Path, m.LatestContent, nil)
	if err != nil {
		slog.Error("failed to parse latest contents of go.mod file", "path", m.Path, "error", err)
		return ""
	}

	initialRequires := map[string]module.Version{}
	for _, require := range initialFile.Require {
		initialRequires[require.Mod.Path] = require.Mod
	}

	latestRequires := map[string]module.Version{}
	for _, require := range latestFile.Require {
		latestRequires[require.Mod.Path] = require.Mod
	}

	var added, removed, bumped []string

	// Find added and bumped packages
	for path, latestMod := range latestRequires {
		initialMod, existed := initialRequires[path]
		if !existed {
			added = append(added, path+"@"+latestMod.Version)
		} else if initialMod.Version != latestMod.Version {
			bumped = append(bumped, path+": "+initialMod.Version+" => "+latestMod.Version)
		}
	}

	// Find removed packages
	for path, initialMod := range initialRequires {
		if _, exists := latestRequires[path]; !exists {
			removed = append(removed, path+"@"+initialMod.Version)
		}
	}

	if len(added) == 0 && len(removed) == 0 && len(bumped) == 0 {
		return ""
	}

	builder := &strings.Builder{}

	if len(added) > 0 {
		builder.WriteString("  Added:\n")

		for _, pkg := range added {
			builder.WriteString("    + " + pkg + "\n")
		}
	}

	if len(removed) > 0 {
		builder.WriteString("  Removed:\n")

		for _, pkg := range removed {
			builder.WriteString("    - " + pkg + "\n")
		}
	}

	if len(bumped) > 0 {
		builder.WriteString("  Version changes:\n")

		for _, pkg := range bumped {
			builder.WriteString("    ~ " + pkg + "\n")
		}
	}

	return builder.String()
}
