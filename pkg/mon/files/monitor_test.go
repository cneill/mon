package files_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cneill/mon/pkg/mon/files"
)

func TestMonitor_CreatingFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	var numToCreate int64 = 200

	monitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  tempDir,
		WatchRoot: true,
	})
	if err != nil {
		t.Fatalf("failed to start file monitor: %v", err)
	}

	defer monitor.Close()

	// Drain events
	go func() {
		for range monitor.Events {
			continue
		}
	}()

	go monitor.Run(context.Background())

	for fileNum := range numToCreate {
		fileName := filepath.Join(tempDir, fmt.Sprintf("temp_file_%d.txt", fileNum))
		if _, err := os.Create(fileName); err != nil {
			t.Fatalf("failed to create temporary file %q: %v", fileName, err)
		}
	}

	// Need to wait for all events to be read...?
	time.Sleep(time.Millisecond * 10)

	stats := monitor.Stats(true)

	if stats.NumFilesCreated != numToCreate {
		t.Errorf("expected %d in NumFilesCreated, got %d", numToCreate, stats.NumFilesCreated)
	}

	if n := int64(len(stats.NewFiles)); n != numToCreate {
		t.Errorf("expected %d items in NewFiles, got %d", numToCreate, n)
	}
}

func TestMonitor_DeletingFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	numToCreate := 200
	toDelete := []string{}

	for fileNum := range numToCreate {
		fileName := filepath.Join(tempDir, fmt.Sprintf("temp_file_%d.txt", fileNum))
		if _, err := os.Create(fileName); err != nil {
			t.Fatalf("failed to create temporary file %q: %v", fileName, err)
		}

		if fileNum%2 == 0 {
			toDelete = append(toDelete, fileName)
		}
	}

	monitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  tempDir,
		WatchRoot: true,
	})
	if err != nil {
		t.Fatalf("failed to start file monitor: %v", err)
	}

	defer monitor.Close()

	// Drain events
	go func() {
		for range monitor.Events {
			continue
		}
	}()

	go monitor.Run(context.Background())

	for _, fileName := range toDelete {
		if err := os.Remove(fileName); err != nil {
			t.Fatalf("failed to delete file %q: %v", fileName, err)
		}
	}

	// Allow pending deletes to settle
	time.Sleep(time.Millisecond * 500)

	stats := monitor.Stats(true)
	numToDelete := int64(len(toDelete))

	if stats.NumFilesDeleted != numToDelete {
		t.Errorf("expected %d in NumFilesDeleted, got %d", numToDelete, stats.NumFilesDeleted)
	}

	if n := int64(len(stats.DeletedFiles)); n != numToDelete {
		t.Errorf("expected %d items in DeletedFiles, got %d", numToDelete, n)
	}
}

// TODO: Find a watcher library that will handle recursive watches - currently, `mkdir -p` equivalents will fail because
// the request to watch the newly created directory happens too slowly to catch the nested dirs created underneath it
func TestMonitor_NewDir(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	monitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  tempDir,
		WatchRoot: true,
	})
	if err != nil {
		t.Fatalf("failed to start file monitor: %v", err)
	}

	defer monitor.Close()

	testFile := filepath.Join(tempDir, "test_file.txt")
	nestedDir := filepath.Join(tempDir, "path", "to", "new", "dir")
	nestedTestFile := filepath.Join(nestedDir, "nested_test_file.txt")

	// Drain events
	go func() {
		for event := range monitor.Events {
			fmt.Printf("%q event for %q\n", event.Op.String(), event.Name)
		}
	}()

	go monitor.Run(context.Background())

	if _, err := os.Create(testFile); err != nil {
		t.Fatalf("failed to create test file %q: %v", testFile, err)
	}

	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("failed to create nested directory %q in temp dir: %v", nestedDir, err)
	}

	// Make sure we have started monitoring all the nested directories first...
	time.Sleep(time.Millisecond * 500)

	if _, err := os.Create(nestedTestFile); err != nil {
		t.Fatalf("failed to create test file %q in nested dir: %v", nestedTestFile, err)
	}

	time.Sleep(time.Millisecond * 200)

	stats := monitor.Stats(true)

	if stats.NumFilesCreated != 6 {
		t.Errorf("expected NumFilesCreated == 6, got %d", stats.NumFilesCreated)
	}

	if n := len(stats.NewFiles); n != 6 {
		t.Errorf("expected length of NewFiles == 6, got %d", n)
	}

	if !slices.Contains(stats.NewFiles, testFile) {
		t.Errorf("missing file %q in NewFiles", testFile)
	}

	if !slices.Contains(stats.NewFiles, nestedTestFile) {
		t.Errorf("missing file %q in NewFiles (%+v)", nestedTestFile, stats.NewFiles)
	}

	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to delete test file %q: %v", testFile, err)
	}

	// Make sure we process the delete event...
	time.Sleep(time.Millisecond * 500)

	stats = monitor.Stats(true)

	if stats.NumFilesCreated != 5 {
		t.Errorf("expected NumFilesCreated == 5 after delete, got %d", stats.NumFilesCreated)
	}

	if n := len(stats.NewFiles); n != 5 {
		t.Errorf("expected length of NewFiles == 5 after delete, got %d", n)
	}

	if slices.Contains(stats.NewFiles, testFile) {
		t.Errorf("file %q was still present in NewFiles after delete", testFile)
	}

	if !slices.Contains(stats.NewFiles, nestedTestFile) {
		t.Errorf("file %q was not present in NewFiles after delete", nestedTestFile)
	}
}
