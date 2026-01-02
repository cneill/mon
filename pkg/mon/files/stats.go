package files

type Stats struct {
	NumFilesCreated int64
	NumFilesDeleted int64
	NewFiles        []string
	DeletedFiles    []string
}

func (m *Monitor) Stats(final bool) *Stats {
	stats := &Stats{
		NumFilesCreated: m.fileMap.filesCreated,
		NumFilesDeleted: m.fileMap.filesDeleted,
	}

	if final {
		stats.NewFiles = m.fileMap.NewFiles()
		stats.DeletedFiles = m.fileMap.DeletedFiles()
	}

	return stats
}
