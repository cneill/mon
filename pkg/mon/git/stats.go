package git

import (
	"log/slog"
	"slices"

	"github.com/go-git/go-git/v5/plumbing/object"
)

type Stats struct {
	NumCommits      int64
	LinesAdded      int64
	LinesDeleted    int64
	UnstagedChanges int64

	Commits []*object.Commit
	Patch   *object.Patch
}

func (m *Monitor) Stats(final bool) *Stats {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	stats := &Stats{
		NumCommits:      m.numCommits,
		LinesAdded:      m.linesAdded,
		LinesDeleted:    m.linesDeleted,
		UnstagedChanges: m.unstagedChanges,
	}

	if final {
		commits, err := CommitsSince(m.repo, m.initialHash)
		if err != nil {
			slog.Error("failed to collect commits since initial hash", "initial_hash", m.initialHash, "error", err)
		}

		slices.Reverse(commits)

		stats.Commits = commits

		patch, err := PatchSince(m.repo, m.initialHash)
		if err != nil {
			slog.Error("failed to generate patch since initial hash", "initial_hash", m.initialHash, "error", err)
		}

		stats.Patch = patch
	}

	return stats
}
