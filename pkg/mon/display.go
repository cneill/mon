package mon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
)

const clearLine = "\r\033[K" // Carriage return + clear to end of line
var (
	gray      = color.RGB(50, 50, 50)
	separator = gray.Sprint(" ][ ")
)

func (m *Mon) displayLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-m.displayChan:
		case <-ticker.C:
		}

		snapshot := m.getStatusSnapshot()

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

func (m *Mon) getStatusSnapshot() *statusSnapshot {
	return &statusSnapshot{
		FilesCreated:    strconv.FormatInt(m.filesCreated.Load(), 10),
		FilesDeleted:    strconv.FormatInt(m.filesDeleted.Load(), 10),
		Commits:         strconv.FormatInt(m.commits.Load(), 10),
		LinesAdded:      strconv.FormatInt(m.linesAdded.Load(), 10),
		LinesDeleted:    strconv.FormatInt(m.linesDeleted.Load(), 10),
		UnstagedChanges: strconv.FormatInt(m.unstagedChanges.Load(), 10),
	}
}

type statusSnapshot struct {
	FilesCreated    string
	FilesDeleted    string
	Commits         string
	LinesAdded      string
	LinesDeleted    string
	UnstagedChanges string
}

func (s *statusSnapshot) String() string {
	builder := &strings.Builder{}
	builder.WriteString("Files: ")
	builder.WriteString(color.GreenString("+" + s.FilesCreated))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString("-" + s.FilesDeleted))
	builder.WriteString(separator)
	builder.WriteString("Commits: ")
	builder.WriteString(color.YellowString(s.Commits))
	builder.WriteString(separator)
	builder.WriteString("Lines: ")
	builder.WriteString(color.GreenString("+" + s.LinesAdded))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString("-" + s.LinesDeleted))

	if s.UnstagedChanges != "0" {
		builder.WriteString(separator)
		builder.WriteString("Unstaged file changes: ")
		builder.WriteString(color.CyanString(s.UnstagedChanges))
	}

	return builder.String()
}

func (s *statusSnapshot) Final() string {
	builder := &strings.Builder{}

	builder.WriteString("Session stats:\n")

	builder.WriteString(" - Files: ")
	builder.WriteString(color.GreenString(s.FilesCreated + " created"))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString(s.FilesDeleted + " deleted"))
	builder.WriteRune('\n')

	builder.WriteString(" - Commits: " + color.YellowString("+"+s.Commits) + "\n")

	builder.WriteString(" - Lines: ")
	builder.WriteString(color.GreenString(s.LinesAdded + " added"))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString(s.LinesDeleted + " deleted"))
	builder.WriteRune('\n')

	if s.UnstagedChanges != "0" {
		builder.WriteString(" - Unstaged file changes: ")
		builder.WriteString(color.CyanString(s.UnstagedChanges))
		builder.WriteRune('\n')
	}

	return builder.String()
}
