package mon

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cneill/mon/pkg/git"
	"github.com/fatih/color"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const clearLine = "\r\033[K" // Carriage return + clear to end of line

//nolint:gochecknoglobals
var (
	labelColor     = color.RGB(255, 255, 255).Add(color.Bold)
	separatorColor = color.RGB(50, 50, 50).Add(color.Bold)
	addedColor     = color.RGB(0, 255, 0)
	removedColor   = color.RGB(255, 0, 0)
	separator      = separatorColor.Sprint(" ][ ")
)

func (m *Mon) displayLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-m.displayChan:
		case <-ticker.C:
		}

		snapshot := m.getStatusSnapshot(false)

		fmt.Printf("%s%s", clearLine, snapshot.String())
		os.Stdout.Sync()
	}
}

func (m *Mon) triggerDisplay() {
	select {
	case m.displayChan <- struct{}{}:
	default:
	}
}

func (m *Mon) getStatusSnapshot(final bool) *statusSnapshot {
	snapshot := &statusSnapshot{
		FilesCreated:    strconv.FormatInt(m.filesCreated.Load(), 10),
		FilesDeleted:    strconv.FormatInt(m.filesDeleted.Load(), 10),
		NumCommits:      strconv.FormatInt(m.commits.Load(), 10),
		LinesAdded:      strconv.FormatInt(m.linesAdded.Load(), 10),
		LinesDeleted:    strconv.FormatInt(m.linesDeleted.Load(), 10),
		UnstagedChanges: strconv.FormatInt(m.unstagedChanges.Load(), 10),
	}

	if final {
		commits, err := git.CommitsSince(m.repo, m.initialHash)
		if err != nil {
			slog.Error("failed to collect commits since initial hash", "initial_hash", m.initialHash, "error", err)
		}

		snapshot.Commits = commits

		patch, err := git.PatchSince(m.repo, m.initialHash)
		if err != nil {
			slog.Error("failed to generate patch since initial hash", "initial_hash", m.initialHash, "error", err)
		}

		snapshot.Patch = patch
	}

	return snapshot
}

type statusSnapshot struct {
	FilesCreated    string
	FilesDeleted    string
	NumCommits      string
	LinesAdded      string
	LinesDeleted    string
	UnstagedChanges string
	Commits         []*object.Commit
	Patch           *object.Patch
}

func (s *statusSnapshot) String() string {
	builder := &strings.Builder{}
	builder.WriteString(labelColor.Sprint("Files: "))
	builder.WriteString(addedColor.Sprint("+" + s.FilesCreated))
	builder.WriteString(" / ")
	builder.WriteString(removedColor.Sprint("-" + s.FilesDeleted))
	builder.WriteString(separator)
	builder.WriteString(labelColor.Sprint("Lines committed: "))
	builder.WriteString(addedColor.Sprint("+" + s.LinesAdded))
	builder.WriteString(" / ")
	builder.WriteString(removedColor.Sprint("-" + s.LinesDeleted))
	builder.WriteString(separator)
	builder.WriteString(labelColor.Sprint("Commits: "))
	builder.WriteString(color.YellowString(s.NumCommits))

	if s.UnstagedChanges != "0" {
		builder.WriteString(separator)
		builder.WriteString(labelColor.Sprint("Unstaged file changes: "))
		builder.WriteString(color.CyanString(s.UnstagedChanges))
	}

	return builder.String()
}

func (s *statusSnapshot) Final() string {
	builder := &strings.Builder{}

	builder.WriteString("Session stats:\n")

	builder.WriteString("\t-Files: ")
	builder.WriteString(addedColor.Sprint(s.FilesCreated + " created"))
	builder.WriteString(" / ")
	builder.WriteString(removedColor.Sprint(s.FilesDeleted + " deleted"))
	builder.WriteRune('\n')

	builder.WriteString("\t- Commits: " + color.YellowString("+"+s.NumCommits) + "\n")

	builder.WriteString("\t- Lines: ")
	builder.WriteString(addedColor.Sprint(s.LinesAdded + " added"))
	builder.WriteString(" / ")
	builder.WriteString(removedColor.Sprint(s.LinesDeleted + " deleted"))
	builder.WriteRune('\n')

	if s.UnstagedChanges != "0" {
		builder.WriteString(" - Unstaged file changes: ")
		builder.WriteString(color.CyanString(s.UnstagedChanges))
		builder.WriteRune('\n')
	}

	builder.WriteString(s.patchString())
	builder.WriteString(s.commitsString())

	return builder.String()
}

func (s *statusSnapshot) patchString() string {
	if s.Patch == nil {
		return ""
	}

	return "\nPatch stats:\n\n" + s.Patch.Stats().String()
}

func (s *statusSnapshot) commitsString() string {
	if s.Commits == nil {
		return ""
	}

	builder := &strings.Builder{}
	builder.WriteString("\nCommits:\n\n")

	for _, commit := range s.Commits {
		msg := "<empty message>"

		msgParts := strings.Split(commit.Message, "\n")
		if len(msgParts) > 0 {
			msg = msgParts[0]
		}

		builder.WriteString("\t- ")
		builder.WriteString(addedColor.Add(color.Bold).Sprint(commit.ID().String()))
		builder.WriteString(": ")
		builder.WriteString(msg)
	}

	return builder.String()
}
