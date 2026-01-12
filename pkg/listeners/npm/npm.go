package npm

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cneill/mon/pkg/deps"
	"github.com/cneill/mon/pkg/listeners"
)

type Listener struct {
	mutex        sync.RWMutex
	packageFiles []*PackageFile
}

func New() *Listener {
	return &Listener{
		packageFiles: []*PackageFile{},
	}
}

func (l *Listener) Name() string { return "Node.JS" }

func (l *Listener) WatchedFiles() []string {
	return []string{
		"package.json",
	}
}

func (l *Listener) LogEvent(event listeners.Event) error {
	base := filepath.Base(event.Name)

	switch event.Type {
	case listeners.EventInit:
		if base == "package.json" {
			slog.Debug("got write event for package.json file", "path", event.Name)
			l.mutex.Lock()
			l.packageFiles = append(l.packageFiles, &PackageFile{
				Path:           event.Name,
				InitialContent: event.Content,
				LatestContent:  event.Content,
			})
			l.mutex.Unlock()
		}

	case listeners.EventWrite:
		if base == "package.json" {
			for _, pkgFile := range l.packageFiles {
				if pkgFile.Path == event.Name {
					slog.Debug("got write event for package.json file", "path", event.Name)

					l.mutex.Lock()

					pkgFile.LatestContent = event.Content

					l.mutex.Unlock()
				}
			}
		}
	}

	return nil
}

func (l *Listener) Diff() string {
	builder := &strings.Builder{}

	for _, pkgFile := range l.packageFiles {
		diff := pkgFile.Diff()
		if diff != "" {
			builder.WriteString(pkgFile.Path + ":\n")
			builder.WriteString(diff + "\n\n")
		}
	}

	return builder.String()
}

type PackageFile struct {
	Path           string
	InitialContent []byte
	LatestContent  []byte
}

func (p *PackageFile) Diff() string {
	var parsedInitial PackageJSON
	if err := json.Unmarshal(p.InitialContent, &parsedInitial); err != nil {
		slog.Error("initial package.json file invalid", "error", err)
		return ""
	}

	initialDeps := parsedInitial.ToDeps()

	var parsedLatest PackageJSON
	if err := json.Unmarshal(p.LatestContent, &parsedLatest); err != nil {
		slog.Error("latest package.json file invalid", "error", err)
		return ""
	}

	latestDeps := parsedLatest.ToDeps()

	return latestDeps.Diff(initialDeps)
}

// TODO: add/shift to package-lock.json

type PackageJSON struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	// Main            string            `json:"main"`
	// Type            string            `json:"type"`
	// Keywords        []string          `json:"keywords"`
	// Author          string            `json:"author"`
	// License         string            `json:"license"`
	// Homepage        string            `json:"homepage"`
	// Scripts         map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
	// DevDependencies map[string]string `json:"devDependencies"`
	// Repository      struct {
	// 	Type string `json:"type"`
	// 	URL  string `json:"url"`
	// } `json:"repository"`
}

func (p *PackageJSON) ToDeps() deps.Dependencies {
	results := make(deps.Dependencies, len(p.Dependencies))
	depIdx := 0

	for identifier, version := range p.Dependencies {
		dep := deps.Dependency{
			Version: version,
		}

		parsedURL, err := url.Parse(identifier)
		if err == nil && parsedURL.Scheme != "" {
			dep.URL = identifier
		} else {
			dep.Name = identifier
		}

		results[depIdx] = dep
		depIdx++
	}

	return results
}
