package mon

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cneill/mon/pkg/listeners"
	"github.com/fatih/color"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const clearLine = "\r\033[K" // Carriage return + clear to end of line

//nolint:gochecknoglobals
var (
	labelColor     = color.RGB(255, 255, 255).Add(color.Bold)
	sublabelColor  = color.RGB(120, 120, 120).Add(color.Italic)
	addedColor     = color.RGB(0, 255, 0)
	removedColor   = color.RGB(255, 0, 0)
	updatedColor   = color.RGB(255, 255, 0)
	separatorColor = color.RGB(50, 50, 50).Add(color.Bold)
	separator      = separatorColor.Sprint(" :: ")
	detailColor    = color.RGB(26, 178, 255)
	indent         = "  "
)

func (m *Mon) displayLoop(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-m.displayChan:
			if !ok {
				return
			}
		case <-ticker.C:
		}

		snapshot := m.GetStatusSnapshot(false)

		fmt.Printf("%s%s", clearLine, snapshot.Live())
		os.Stdout.Sync()
	}
}

func (m *Mon) triggerDisplay() {
	select {
	case m.displayChan <- struct{}{}:
	default:
	}
}

type StatusSnapshot struct {
	*DetailsOpts

	NumFilesCreated int64            `json:"num_files_created"`
	NumFilesDeleted int64            `json:"num_files_deleted"`
	NewFiles        []string         `json:"new_file_paths"`
	DeletedFiles    []string         `json:"deleted_file_paths"`
	WrittenFiles    map[string]int64 `json:"file_writes"`

	NumCommits      int64            `json:"num_commits"`
	LinesAdded      int64            `json:"lines_added"`
	LinesDeleted    int64            `json:"lines_deleted"`
	UnstagedChanges int64            `json:"unstaged_changes"`
	Commits         []*object.Commit `json:"-"`
	Patch           *object.Patch    `json:"-"`

	StartTime time.Time `json:"start_time"`
	LastWrite time.Time `json:"last_write"`

	ListenerDiffs map[string]listeners.Diff `json:"-"`
}

func (m *Mon) GetStatusSnapshot(final bool) *StatusSnapshot {
	fileStats := m.fileMonitor.Stats(final)
	slices.Sort(fileStats.NewFiles)
	slices.Sort(fileStats.DeletedFiles)

	gitStats := m.gitMonitor.Stats(final)
	slices.Reverse(gitStats.Commits)

	snapshot := &StatusSnapshot{
		DetailsOpts: m.DetailsOpts,

		NumFilesCreated: fileStats.NumFilesCreated,
		NumFilesDeleted: fileStats.NumFilesDeleted,
		NewFiles:        fileStats.NewFiles,
		DeletedFiles:    fileStats.DeletedFiles,
		WrittenFiles:    fileStats.WrittenFiles,

		NumCommits:      gitStats.NumCommits,
		LinesAdded:      gitStats.LinesAdded,
		LinesDeleted:    gitStats.LinesDeleted,
		UnstagedChanges: gitStats.UnstagedChanges,
		Commits:         gitStats.Commits,
		Patch:           gitStats.Patch,

		StartTime: m.startTime,
		LastWrite: m.lastWrite,

		ListenerDiffs: map[string]listeners.Diff{},
	}

	if final {
		for _, listener := range m.listeners {
			snapshot.ListenerDiffs[listener.Name()] = listener.Diff()
		}
	}

	return snapshot
}

func (s *StatusSnapshot) Live() string {
	builder := &strings.Builder{}
	builder.Grow(64)

	builder.WriteString(labelColor.Sprint("[F] "))
	builder.WriteString(addedColor.Sprint("+" + strconv.FormatInt(s.NumFilesCreated, 10)))
	builder.WriteString(" / ")
	builder.WriteString(removedColor.Sprint("-" + strconv.FormatInt(s.NumFilesDeleted, 10)))
	builder.WriteString(separator)
	builder.WriteString(labelColor.Sprint("[L] "))
	builder.WriteString(addedColor.Sprint("+" + strconv.FormatInt(s.LinesAdded, 10)))
	builder.WriteString(" / ")
	builder.WriteString(removedColor.Sprint("-" + strconv.FormatInt(s.LinesDeleted, 10)))
	builder.WriteString(separator)
	builder.WriteString(labelColor.Sprint("[C] "))
	builder.WriteString(addedColor.Sprint(s.NumCommits))

	if s.UnstagedChanges > 0 {
		builder.WriteString(separator)
		builder.WriteString(labelColor.Sprint("[!] "))
		builder.WriteString(addedColor.Sprint(s.UnstagedChanges))
	}

	if since := time.Since(s.LastWrite); !s.LastWrite.IsZero() && since > time.Minute {
		builder.WriteString(separator)
		builder.WriteString(labelColor.Sprint("[~] "))
		builder.WriteString(sublabelColor.Sprint(durationString(since)))
	}

	return builder.String()
}

func (s *StatusSnapshot) Final() string {
	builder := &strings.Builder{}
	builder.Grow(64)

	builder.WriteString(labelColor.Sprint("Session stats:\n"))

	builder.WriteString(indent)
	builder.WriteString(sublabelColor.Sprint("Duration: "))
	builder.WriteString(detailColor.Sprint(durationString(time.Since(s.StartTime))))
	builder.WriteRune('\n')

	builder.WriteString(indent)
	builder.WriteString(sublabelColor.Sprint("Files: "))
	builder.WriteString(addedColor.Sprint(strconv.FormatInt(s.NumFilesCreated, 10) + " created"))
	builder.WriteString(separator)
	builder.WriteString(removedColor.Sprint(strconv.FormatInt(s.NumFilesDeleted, 10) + " deleted"))
	builder.WriteRune('\n')

	builder.WriteString(indent)
	builder.WriteString(sublabelColor.Sprint("Commits: "))
	builder.WriteString(addedColor.Sprint(s.NumCommits))
	builder.WriteRune('\n')

	builder.WriteString(indent)
	builder.WriteString(sublabelColor.Sprint("Lines: "))
	builder.WriteString(addedColor.Sprint(strconv.FormatInt(s.LinesAdded, 10) + " added"))
	builder.WriteString(separator)
	builder.WriteString(removedColor.Sprint(strconv.FormatInt(s.LinesDeleted, 10) + " deleted"))
	builder.WriteRune('\n')

	if s.UnstagedChanges > 0 {
		builder.WriteString(indent)
		builder.WriteString(sublabelColor.Sprint("Unstaged file changes: "))
		builder.WriteString(addedColor.Sprint(s.UnstagedChanges))
		builder.WriteRune('\n')
	}

	if s.ShowAllFiles {
		builder.WriteString(s.filesString())
	}

	builder.WriteString(s.patchString())
	builder.WriteString(s.commitsString())
	builder.WriteString(s.listenersString())

	return builder.String()
}

func (s *StatusSnapshot) filesString() string {
	builder := &strings.Builder{}
	builder.Grow(256)

	if len(s.NewFiles) > 0 {
		builder.WriteString(labelColor.Sprint("\nNew files:\n"))

		for _, file := range s.NewFiles {
			builder.WriteString(indent + sublabelColor.Sprint(file) + "\n")
		}
	}

	if len(s.DeletedFiles) > 0 {
		builder.WriteString(labelColor.Sprint("\nDeleted files:\n"))

		for _, file := range s.DeletedFiles {
			builder.WriteString(indent + sublabelColor.Sprint(file) + "\n")
		}
	}

	if len(s.WrittenFiles) > 0 {
		builder.WriteString(labelColor.Sprint("\nWritten files:\n"))

		files := slices.Collect(maps.Keys(s.WrittenFiles))
		slices.Sort(files)

		for _, file := range files {
			writes := strconv.FormatInt(s.WrittenFiles[file], 10)
			builder.WriteString(indent + sublabelColor.Sprint(file) + separator + detailColor.Sprint(writes) + "\n")
		}
	}

	return builder.String()
}

func (s *StatusSnapshot) patchString() string {
	if s.Patch == nil || s.NumCommits == 0 {
		return ""
	}

	// Borrowed from go-git
	// https://github.com/go-git/go-git/blob/de8ecc3b52e6a37b24a5a8ca362b54cafed2bc0b/plumbing/object/patch.go#L237-L287
	maxChangeWidth := 80
	scaleChangeSize := func(num, total int) int {
		if num == 0 || total == 0 {
			return 0
		}

		return 1 + (num * (maxChangeWidth - 1) / total)
	}

	stats := s.Patch.Stats()

	builder := &strings.Builder{}
	builder.Grow(256)
	builder.WriteString(labelColor.Sprint("\nPatch stats:\n"))

	for _, fileStats := range stats {
		totalChanges := fileStats.Addition + fileStats.Deletion
		totalChangesStr := strconv.FormatInt(int64(totalChanges), 10)

		builder.WriteString(indent)
		builder.WriteString(sublabelColor.Sprint(fileStats.Name))
		builder.WriteString(separator)
		builder.WriteString(totalChangesStr)
		builder.WriteRune(' ')

		adds := fileStats.Addition
		deletes := fileStats.Deletion

		if totalChanges > maxChangeWidth {
			adds = scaleChangeSize(adds, totalChanges)
			deletes = scaleChangeSize(deletes, totalChanges)
		}

		builder.WriteString(addedColor.Sprint(strings.Repeat("+", adds)))
		builder.WriteString(removedColor.Sprint(strings.Repeat("-", deletes)))
		builder.WriteRune('\n')
	}

	return builder.String()
}

func (s *StatusSnapshot) commitsString() string {
	if s.Commits == nil {
		return ""
	}

	builder := &strings.Builder{}
	builder.Grow(256)
	builder.WriteString(labelColor.Sprint("\nCommits:\n"))

	for _, commit := range s.Commits {
		msg := "<empty message>"

		msgParts := strings.Split(commit.Message, "\n")
		if len(msgParts) > 0 {
			msg = msgParts[0]
		}

		builder.WriteString(indent)
		builder.WriteString(sublabelColor.Sprint(commit.ID().String()))
		builder.WriteString(separator)
		builder.WriteString(detailColor.Sprint(commit.Committer.When.Format(time.RFC3339)))
		builder.WriteString(separator)
		builder.WriteString(msg)
		builder.WriteRune('\n')
	}

	return builder.String()
}

func (s *StatusSnapshot) listenersString() string {
	builder := &strings.Builder{}
	builder.Grow(128)

	for listener, diff := range s.ListenerDiffs {
		if diff.IsEmpty() {
			continue
		}

		builder.WriteString(labelColor.Sprint(listener + ":\n"))
		builder.WriteString(s.listenerDependencyString(diff))
	}

	return builder.String()
}

func (s *StatusSnapshot) listenerDependencyString(diff listeners.Diff) string {
	builder := &strings.Builder{}
	builder.Grow(64)

	for _, fileDiff := range diff.DependencyFileDiffs {
		if fileDiff.IsEmpty() {
			continue
		}

		builder.WriteString(indent + sublabelColor.Sprint(fileDiff.Path) + ":\n")

		if len(fileDiff.NewDependencies) > 0 {
			for _, dep := range fileDiff.NewDependencies {
				builder.WriteString(indent + indent)
				builder.WriteString(addedColor.Sprint("+") + " ")
				builder.WriteString(detailColor.Sprint(dep.String()))
				builder.WriteRune('\n')
			}
		}

		if len(fileDiff.DeletedDependencies) > 0 {
			for _, dep := range fileDiff.DeletedDependencies {
				builder.WriteString(indent + indent)
				builder.WriteString(removedColor.Sprint("-") + " ")
				builder.WriteString(detailColor.Sprint(dep.String()))
				builder.WriteRune('\n')
			}
		}

		if len(fileDiff.UpdatedDependencies) > 0 {
			for _, dep := range fileDiff.UpdatedDependencies {
				builder.WriteString(indent + indent)
				builder.WriteString(updatedColor.Sprint("~") + " ")
				builder.WriteString(dep.Initial.Package() + separator)
				builder.WriteString(removedColor.Sprint(dep.Initial.Version))
				builder.WriteString(updatedColor.Sprint(" => "))
				builder.WriteString(addedColor.Sprint(dep.Latest.Version))
				builder.WriteRune('\n')
			}
		}
	}

	return builder.String()
}

func durationString(duration time.Duration) string {
	result := ""
	hours := int64(duration / time.Hour)
	minutes := int64((duration - (time.Duration(hours) * time.Hour)) / time.Minute)
	seconds := int64((duration - (time.Duration(hours) * time.Hour) - (time.Duration(minutes) * time.Minute)) / time.Second)

	if hours > 0 {
		result += strconv.FormatInt(hours, 10) + "h"
	}

	if minutes > 0 {
		result += strconv.FormatInt(minutes, 10) + "m"
	}

	result += strconv.FormatInt(seconds, 10) + "s"

	return result
}
