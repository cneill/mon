package files

import (
	"errors"
	"io/fs"
	"path/filepath"
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

	FileType      FileType
	WasDeleted    bool // This is to track the deletion of initial files. New files will be removed from the map with Delete()
	Writes        int64
	PreSwapWrites int64 // Writes that occurred before editor swaps (not counted in final total)
	PendingSwap   bool  // True if file has a pending delete that might be part of an editor swap
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

func (f *FileMap) AddWrite(name string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	file, ok := f.files[name]
	if !ok {
		return ErrUnknownFile
	}

	// Don't count writes that happen right before a swap - the swap will be counted instead
	if file.PendingSwap {
		return nil
	}

	file.Writes++

	return nil
}

// AddSwapWrite records a write from an editor swap (delete+create pair).
// It also clears any writes that occurred just before the swap to avoid double-counting.
func (f *FileMap) AddSwapWrite(name string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	file, ok := f.files[name]
	if !ok {
		return ErrUnknownFile
	}

	// Clear pre-swap writes and count the swap as a single write
	file.PreSwapWrites += file.Writes
	file.Writes = 1
	file.PendingSwap = false

	return nil
}

// MarkPendingSwap marks a file as potentially being swapped by an editor.
// This prevents writes from being counted until we know if a swap occurred.
func (f *FileMap) MarkPendingSwap(name string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if file, ok := f.files[name]; ok {
		file.PendingSwap = true
	}
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

func (f *FileMap) FilePathsByBase(name string) []string {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	results := []string{}

	for file := range f.files {
		if base := filepath.Base(file); base == name {
			results = append(results, file)
		}
	}

	return results
}

func (f *FileMap) NewFiles() []string {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	results := make([]string, 0, len(f.files))

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

	results := make([]string, 0, len(f.files))

	for name, info := range f.files {
		if info.WasDeleted {
			results = append(results, name)
		}
	}

	return results
}

func (f *FileMap) WrittenFiles() map[string]int64 {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	results := map[string]int64{}

	for name, info := range f.files {
		if info.Writes > 0 {
			results[name] = info.Writes
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
