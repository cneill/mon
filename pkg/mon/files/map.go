package files

import (
	"errors"
	"io/fs"
	"sync"
)

var (
	ErrUnknownFile = errors.New("unknown file name")
	ErrFileTracked = errors.New("file already tracked")
)

type FileType string

const (
	FileTypeInitial FileType = "initial"
	FileTypeNew     FileType = "new"
)

type FileInfo struct {
	fs.FileInfo

	FileType   FileType
	WasDeleted bool // This is to track the deletion of initial files. New files will be removed from the map with Delete()
}

func (f FileInfo) IsInitial() bool { return f.FileType == FileTypeInitial }

type FileMap struct {
	files map[string]*FileInfo
	mutex sync.RWMutex

	filesCreated int64
	filesDeleted int64
}

func NewFileMap() *FileMap {
	return &FileMap{
		files: map[string]*FileInfo{},
	}
}

func (f *FileMap) AddFile(name string, info FileInfo) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	file, ok := f.files[name]
	if ok {
		if !file.WasDeleted {
			return ErrFileTracked
		}

		file.WasDeleted = false

		if !file.IsInitial() {
			f.filesCreated++
		}
	} else if info.FileType != FileTypeInitial {
		f.filesCreated++
	}

	f.files[name] = &info

	return nil
}

func (f *FileMap) IsInitial(name string) bool {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	file, ok := f.files[name]
	if !ok {
		return false
	}

	return file.IsInitial()
}

func (f *FileMap) Delete(name string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	file, ok := f.files[name]
	if !ok {
		return ErrUnknownFile
	}

	if file.IsInitial() {
		file.WasDeleted = true
		f.filesDeleted++
	} else {
		delete(f.files, name)
		f.filesCreated--
	}

	return nil
}

func (f *FileMap) Has(name string) bool {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	_, ok := f.files[name]

	return ok
}

func (f *FileMap) Get(name string) (FileInfo, error) {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	file, ok := f.files[name]
	if !ok {
		return FileInfo{}, ErrUnknownFile
	}

	return *file, nil
}

func (f *FileMap) NewFiles() []string {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	results := []string{}

	for name, info := range f.files {
		if info.FileType == FileTypeNew {
			results = append(results, name)
		}
	}

	return results
}

func (f *FileMap) DeletedFiles() []string {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	results := []string{}

	for name, info := range f.files {
		if info.WasDeleted {
			results = append(results, name)
		}
	}

	return results
}

func (f *FileMap) FilesCreated() int64 {
	f.mutex.RLock()
	defer f.mutex.RUnlock()
	return f.filesCreated
}

func (f *FileMap) FilesDeleted() int64 {
	f.mutex.RLock()
	defer f.mutex.RUnlock()
	return f.filesDeleted
}
