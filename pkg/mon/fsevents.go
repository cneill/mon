package mon

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/cneill/mon/pkg/mon/files"
)

func (m *Mon) handleFSEvents(ctx context.Context) {
	go m.FileMonitor.Run(ctx)

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-m.FileMonitor.Events:
			if !ok {
				return
			}

			if m.ignoreEvent(event) {
				continue
			}

			if event.Name == m.gitLogPath && (event.Type() == files.EventTypeWrite || event.Type() == files.EventTypeChmod) {
				go m.processGitChange()
				continue
			}

			switch event.Type() {
			case files.EventTypeCreate, files.EventTypeRemove, files.EventTypeRename:
				go m.triggerDisplay()
			case files.EventTypeWrite:
				time.Sleep(time.Millisecond * 250) // allow write+delete pairs to settle before checking

				if m.writeLimiter.Allow() {
					m.writeLimiter.Reserve()

					go m.processGitChange()
				}
			}
		}
	}
}

func (m *Mon) ignoreEvent(event files.Event) bool {
	if strings.Contains(event.Name, ".git/") && event.Name != m.gitLogPath {
		slog.Debug("ignoring file event in .git directory")
		return true
	}

	// Ignore VIM temp files: backups (~, .swp), swap (numeric names)
	base := filepath.Base(event.Name)
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") || isNumeric(base) {
		slog.Debug("ignoring editor file swaps")
		return true
	}

	return false
}

func isNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}

	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}
