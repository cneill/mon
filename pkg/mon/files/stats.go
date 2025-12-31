package files

type Stats struct {
	FilesCreated int64
	FilesDeleted int64
}

func (f *FileMonitor) Stats() *Stats {
	return &Stats{
		FilesCreated: f.fileMap.filesCreated,
		FilesDeleted: f.fileMap.filesDeleted,
	}
}
