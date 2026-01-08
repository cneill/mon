package files

type Stats struct {
	NumFilesCreated int64
	NumFilesDeleted int64
	NewFiles        []string
	DeletedFiles    []string
	WrittenFiles    map[string]int64
}

func (m *Monitor) Stats(final bool) *Stats {
	stats := &Stats{
		NumFilesCreated: m.fileMap.FilesCreated(),
		NumFilesDeleted: m.fileMap.FilesDeleted(),
	}

	if final {
		stats.NewFiles = m.fileMap.NewFiles()
		stats.DeletedFiles = m.fileMap.DeletedFiles()
		stats.WrittenFiles = m.fileMap.WrittenFiles()
	}

	return stats
}
