package mon

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/cneill/mon/pkg/mon/files"
	"github.com/cneill/mon/pkg/mon/git"
)

func (m *Mon) handleFSEvents(ctx context.Context) {
	go m.fileMonitor.Run(ctx)
	defer m.fileMonitor.Close()

	go m.gitMonitor.Run(ctx)
	defer m.gitMonitor.Close()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-m.fileMonitor.Events:
			if !ok {
				slog.Info("file monitor shut down")
				return
			}

			if m.ignoreEvent(event) {
				continue
			}

			switch event.Type() {
			case files.EventTypeCreate, files.EventTypeRemove, files.EventTypeRename:
				go m.triggerDisplay()
			case files.EventTypeWrite:
				time.Sleep(time.Millisecond * 250) // allow write+delete pairs to settle before checking

				if m.writeLimiter.Allow() {
					m.writeLimiter.Reserve()

					m.gitMonitor.FileEvents <- event
				}
			}

		case event, ok := <-m.gitMonitor.GitEvents:
			if !ok {
				slog.Info("git monitor shut down")
				return
			}

			switch event.Type {
			case git.EventTypeNewCommit:
				m.triggerDisplay()
			}
		}
	}
}

func (m *Mon) ignoreEvent(event files.Event) bool {
	// if strings.Contains(event.Name, ".git/") && event.Name != m.gitLogPath {
	// 	slog.Debug("ignoring file event in .git directory")
	// 	return true
	// }

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
