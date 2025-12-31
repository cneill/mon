package files

type Stats struct {
	NumFilesCreated int64
	NumFilesDeleted int64
	NewFiles        []string
	DeletedFiles    []string
}

func (f *FileMonitor) Stats(final bool) *Stats {
	stats := &Stats{
		NumFilesCreated: f.fileMap.filesCreated,
		NumFilesDeleted: f.fileMap.filesDeleted,
	}

	if final {
		stats.NewFiles = f.fileMap.NewFiles()
		stats.DeletedFiles = f.fileMap.DeletedFiles()
	}

	return stats
}
