package files

import "slices"

type Stats struct {
	NumFilesCreated int64
	NumFilesDeleted int64
	NewFiles        []string
	DeletedFiles    []string
}

func (m *Monitor) Stats(final bool) *Stats {
	stats := &Stats{
		NumFilesCreated: m.fileMap.FilesCreated(),
		NumFilesDeleted: m.fileMap.FilesDeleted(),
	}

	if final {
		stats.NewFiles = m.fileMap.NewFiles()
		slices.Sort(stats.NewFiles)

		stats.DeletedFiles = m.fileMap.DeletedFiles()
		slices.Sort(stats.DeletedFiles)
	}

	return stats
}
