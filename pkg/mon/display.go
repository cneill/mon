package mon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
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

		fmt.Printf("\r%s", snapshot.String())
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
		FilesCreated: m.filesCreated.Load(),
		FilesDeleted: m.filesDeleted.Load(),
		Commits:      m.commits.Load(),
		LinesAdded:   m.linesAdded.Load(),
		LinesDeleted: m.linesDeleted.Load(),
	}
}

type statusSnapshot struct {
	FilesCreated int64
	FilesDeleted int64
	Commits      int64
	LinesAdded   int64
	LinesDeleted int64
}

func (s *statusSnapshot) String() string {
	builder := &strings.Builder{}
	builder.WriteString("Files: ")
	builder.WriteString(color.GreenString("+" + strconv.FormatInt(s.FilesCreated, 10)))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString("-" + strconv.FormatInt(s.FilesDeleted, 10)))
	builder.WriteString(" || Commits: ")
	builder.WriteString(color.YellowString(strconv.FormatInt(s.Commits, 10)))
	builder.WriteString(" || Lines: ")
	builder.WriteString(color.GreenString("+" + strconv.FormatInt(s.LinesAdded, 10)))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString("-" + strconv.FormatInt(s.LinesDeleted, 10)))

	return builder.String()
}

func (s *statusSnapshot) Final() string {
	builder := &strings.Builder{}

	builder.WriteString("Session stats:\n")

	builder.WriteString(" - Files: ")
	builder.WriteString(color.GreenString(strconv.FormatInt(s.FilesCreated, 10) + " created"))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString(strconv.FormatInt(s.FilesDeleted, 10) + " deleted"))
	builder.WriteRune('\n')

	builder.WriteString(" - Commits: " + color.YellowString("+"+strconv.FormatInt(s.Commits, 10)) + "\n")

	builder.WriteString(" - Lines: ")
	builder.WriteString(color.GreenString(strconv.FormatInt(s.LinesAdded, 10) + " added"))
	builder.WriteString(" / ")
	builder.WriteString(color.RedString(strconv.FormatInt(s.LinesDeleted, 10) + " deleted"))
	builder.WriteRune('\n')

	return builder.String()
}
