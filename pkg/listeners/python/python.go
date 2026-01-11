package python

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/cneill/mon/pkg/deps"
	"github.com/cneill/mon/pkg/listeners"
)

type Listener struct {
	mutex             sync.RWMutex
	requirementsFiles []*RequirementsFile
	pyprojectFiles    []*PyProjectFile
}

func New() *Listener {
	return &Listener{
		requirementsFiles: []*RequirementsFile{},
		pyprojectFiles:    []*PyProjectFile{},
	}
}

func (l *Listener) Name() string { return "Python" }

func (l *Listener) WatchedFiles() []string {
	return []string{
		"requirements.txt",
		"pyproject.toml",
	}
}

func (l *Listener) LogEvent(event listeners.Event) error {
	base := filepath.Base(event.Name)

	switch base {
	case "requirements.txt":
		return l.handleRequirementsTxt(event)
	case "pyproject.toml":
		return l.handlePyProjectToml(event)
	}

	return nil
}

func (l *Listener) Diff() string {
	builder := &strings.Builder{}

	for _, reqFile := range l.requirementsFiles {
		diff := reqFile.Diff()
		if diff != "" {
			builder.WriteString(reqFile.Path + ":\n")
			builder.WriteString(diff + "\n\n")
		}
	}

	for _, pyFile := range l.pyprojectFiles {
		diff := pyFile.Diff()
		if diff != "" {
			builder.WriteString(pyFile.Path + ":\n")
			builder.WriteString(diff + "\n\n")
		}
	}

	return builder.String()
}

// RequirementsFile tracks a requirements.txt file's initial and latest content.
type RequirementsFile struct {
	Path           string
	InitialContent []byte
	LatestContent  []byte
}

func (r *RequirementsFile) Diff() string {
	if r.LatestContent == nil {
		return ""
	}

	initialDeps := ParseRequirementsTxt(r.InitialContent)
	latestDeps := ParseRequirementsTxt(r.LatestContent)

	return latestDeps.Diff(initialDeps)
}

func (l *Listener) handleRequirementsTxt(event listeners.Event) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	switch event.Type {
	case listeners.EventInit:
		slog.Debug("got init event for requirements.txt file", "path", event.Name)
		l.requirementsFiles = append(l.requirementsFiles, &RequirementsFile{
			Path:           event.Name,
			InitialContent: event.Content,
		})

	case listeners.EventWrite:
		for _, reqFile := range l.requirementsFiles {
			if reqFile.Path == event.Name {
				slog.Debug("got write event for requirements.txt file", "path", event.Name)
				reqFile.LatestContent = event.Content
			}
		}
	}

	return nil
}

func (l *Listener) handlePyProjectToml(event listeners.Event) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	switch event.Type {
	case listeners.EventInit:
		slog.Debug("got init event for pyproject.toml file", "path", event.Name)
		l.pyprojectFiles = append(l.pyprojectFiles, &PyProjectFile{
			Path:           event.Name,
			InitialContent: event.Content,
		})
	case listeners.EventWrite:
		for _, pyFile := range l.pyprojectFiles {
			if pyFile.Path == event.Name {
				slog.Debug("got write event for pyproject.toml file", "path", event.Name)
				pyFile.LatestContent = event.Content
			}
		}
	}

	return nil
}

// PyProjectFile tracks a pyproject.toml file's initial and latest content.
type PyProjectFile struct {
	Path           string
	InitialContent []byte
	LatestContent  []byte
}

func (p *PyProjectFile) Diff() string {
	if p.LatestContent == nil {
		return ""
	}

	initialDeps, err := ParsePyProjectToml(p.InitialContent)
	if err != nil {
		slog.Error("initial pyproject.toml file invalid", "error", err)
		return ""
	}

	latestDeps, err := ParsePyProjectToml(p.LatestContent)
	if err != nil {
		slog.Error("latest pyproject.toml file invalid", "error", err)
		return ""
	}

	return latestDeps.Diff(initialDeps)
}

// ParseRequirementsTxt parses a requirements.txt file into a list of dependencies.
func ParseRequirementsTxt(content []byte) deps.Dependencies {
	var results deps.Dependencies

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines, comments, and directives
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "-r") || strings.HasPrefix(line, "-c") || strings.HasPrefix(line, "-e") {
			continue
		}

		if strings.HasPrefix(line, "--") {
			continue
		}

		dep := parsePEP508(line)
		if dep != nil {
			results = append(results, *dep)
		}
	}

	return results
}

// pyProject represents the structure of pyproject.toml we care about.
type pyProject struct {
	Project struct {
		Dependencies []string `toml:"dependencies"`
	} `toml:"project"`
}

// ParsePyProjectToml parses a pyproject.toml file into a list of dependencies.
func ParsePyProjectToml(content []byte) (deps.Dependencies, error) {
	var proj pyProject
	if err := toml.Unmarshal(content, &proj); err != nil {
		return nil, fmt.Errorf("failed to parse pyproject file: %w", err)
	}

	var results deps.Dependencies

	for _, depStr := range proj.Project.Dependencies {
		dep := parsePEP508(depStr)
		if dep != nil {
			results = append(results, *dep)
		}
	}

	return results, nil
}

// parsePEP508 parses a PEP 508 dependency string into a Dependency.
// Handles formats like:
//   - requests==2.28.0
//   - requests>=2.0,<3.0
//   - requests[security]>=2.0
//   - git+https://github.com/user/repo.git@v1.0
//   - package @ https://example.com/pkg.whl
//   - requests>=2.0 ; python_version >= "3.8"
func parsePEP508(line string) *deps.Dependency {
	// Strip inline comments
	if idx := strings.Index(line, "#"); idx != -1 {
		line = line[:idx]
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	// Strip environment markers (everything after ;)
	if idx := strings.Index(line, ";"); idx != -1 {
		line = strings.TrimSpace(line[:idx])
	}

	// Handle URL-based dependencies (git+https://, https://, etc.)
	if strings.HasPrefix(line, "git+") || strings.HasPrefix(line, "https://") || strings.HasPrefix(line, "http://") {
		return parseURLDependency(line)
	}

	// Handle PEP 440 direct references: package @ URL
	if name, url, found := strings.Cut(line, " @ "); found {
		name := strings.TrimSpace(name)
		url := strings.TrimSpace(url)

		return &deps.Dependency{
			Name: name,
			URL:  url,
		}
	}

	// Parse standard dependency: name[extras]versionspec
	return parseStandardDependency(line)
}

// parseURLDependency handles git+ and direct URL dependencies.
func parseURLDependency(line string) *deps.Dependency {
	dep := &deps.Dependency{
		URL: line,
	}

	// Try to extract version from git URLs (after @)
	// e.g., git+https://github.com/user/repo.git@v1.0.0
	if strings.HasPrefix(line, "git+") {
		if idx := strings.LastIndex(line, "@"); idx != -1 {
			// Make sure it's not the @ in an email or user@host
			afterAt := line[idx+1:]
			if !strings.Contains(afterAt, "/") && !strings.Contains(afterAt, ":") {
				dep.Version = afterAt
			}
		}
	}

	return dep
}

// parseStandardDependency parses a standard name[extras]versionspec dependency.
func parseStandardDependency(line string) *deps.Dependency {
	// Find where version specifier starts
	// Version operators: ==, !=, <=, >=, <, >, ~=, ===
	versionIdx := -1

	for i := range len(line) {
		c := line[i]
		if c == '=' || c == '!' || c == '<' || c == '>' || c == '~' {
			versionIdx = i
			break
		}
	}

	var name, version string

	if versionIdx == -1 {
		// No version specifier
		name = line
	} else {
		name = line[:versionIdx]
		version = line[versionIdx:]
	}

	name = strings.TrimSpace(name)
	version, _ = strings.CutPrefix(strings.TrimSpace(version), "==")

	if name == "" {
		return nil
	}

	return &deps.Dependency{
		Name:    name,
		Version: version,
	}
}
