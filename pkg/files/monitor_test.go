package files_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cneill/mon/pkg/files"
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

	// Drain events
	go func() {
		for range monitor.Events {
			continue
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())

	go monitor.Run(ctx)

	time.Sleep(time.Millisecond * 50)

	for fileNum := range numToCreate {
		fileName := filepath.Join(tempDir, fmt.Sprintf("temp_file_%d.txt", fileNum))
		if _, err := os.Create(fileName); err != nil {
			t.Fatalf("failed to create temporary file %q: %v", fileName, err)
		}
	}

	time.Sleep(time.Millisecond * 100)
	cancel()

	monitor.Close()

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

	// Drain events
	go func() {
		for range monitor.Events {
			continue
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	go monitor.Run(ctx)

	time.Sleep(time.Millisecond * 100)

	for _, fileName := range toDelete {
		if err := os.Remove(fileName); err != nil {
			t.Fatalf("failed to delete file %q: %v", fileName, err)
		}
	}

	// Allow pending deletes to settle
	time.Sleep(time.Millisecond * 500)

	cancel()
	monitor.Close()

	stats := monitor.Stats(true)
	numToDelete := int64(len(toDelete))

	if stats.NumFilesDeleted != numToDelete {
		t.Errorf("expected %d in NumFilesDeleted, got %d", numToDelete, stats.NumFilesDeleted)
	}

	if n := int64(len(stats.DeletedFiles)); n != numToDelete {
		t.Errorf("expected %d items in DeletedFiles, got %d", numToDelete, n)
	}
}

func TestMonitor_NewDir(t *testing.T) { //nolint:cyclop,funlen // not worth breaking this up
	t.Parallel()

	tempDir := t.TempDir()

	monitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  tempDir,
		WatchRoot: true,
	})
	if err != nil {
		t.Fatalf("failed to start file monitor: %v", err)
	}

	testFile := filepath.Join(tempDir, "test_file.txt")
	nestedDir := filepath.Join(tempDir, "path", "to", "new", "dir")
	nestedTestFile := filepath.Join(nestedDir, "nested_test_file.txt")

	// Drain events
	go func() {
		for event := range monitor.Events {
			fmt.Printf("%q event for %q\n", event.Op.String(), event.Name)
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())

	go monitor.Run(ctx)

	time.Sleep(time.Millisecond * 100)

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

	cancel()
	monitor.Close()

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

func TestMonitor_EditorSwapIgnored(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Create an initial file that will be "edited" by our simulated editor
	testFile := filepath.Join(tempDir, "document.txt")
	if err := os.WriteFile(testFile, []byte("initial content"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	monitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  tempDir,
		WatchRoot: true,
	})
	if err != nil {
		t.Fatalf("failed to start file monitor: %v", err)
	}

	// Drain events
	go func() {
		for range monitor.Events {
			continue
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	go monitor.Run(ctx)

	// Let the monitor start up
	time.Sleep(time.Millisecond * 100)

	// Simulate editor swap pattern (like vim or vscode):
	// 1. Write new content to a temp file
	// 2. Delete the original file
	// 3. Rename temp file to original name
	swapFile := filepath.Join(tempDir, "document.txt.swp")

	if err := os.WriteFile(swapFile, []byte("updated content"), 0o644); err != nil {
		t.Fatalf("failed to create swap file: %v", err)
	}

	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to remove original file: %v", err)
	}

	if err := os.Rename(swapFile, testFile); err != nil {
		t.Fatalf("failed to rename swap file to original: %v", err)
	}

	// Wait for pending deletes to be processed (deleteTimeout is 250ms, check interval is 100ms)
	time.Sleep(time.Millisecond * 500)

	cancel()
	monitor.Close()

	stats := monitor.Stats(true)

	// The original file should NOT be counted as deleted since it still exists
	if stats.NumFilesDeleted != 0 {
		t.Errorf("expected NumFilesDeleted == 0 (editor swap should be ignored), got %d", stats.NumFilesDeleted)
	}

	if len(stats.DeletedFiles) != 0 {
		t.Errorf("expected DeletedFiles to be empty, got %v", stats.DeletedFiles)
	}
}

func TestMonitor_EditorSwapMultipleEdits(t *testing.T) { //nolint:cyclop
	t.Parallel()

	tempDir := t.TempDir()

	// Create initial files
	file1 := filepath.Join(tempDir, "file1.txt")
	file2 := filepath.Join(tempDir, "file2.txt")

	for _, f := range []string{file1, file2} {
		if err := os.WriteFile(f, []byte("initial"), 0o644); err != nil {
			t.Fatalf("failed to create test file %q: %v", f, err)
		}
	}

	monitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  tempDir,
		WatchRoot: true,
	})
	if err != nil {
		t.Fatalf("failed to start file monitor: %v", err)
	}

	go func() {
		for range monitor.Events {
			continue
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	go monitor.Run(ctx)

	time.Sleep(time.Millisecond * 100)

	// Simulate multiple rapid editor saves on both files
	for i := range 5 {
		for _, testFile := range []string{file1, file2} {
			swapFile := testFile + ".swp"

			if err := os.WriteFile(swapFile, fmt.Appendf([]byte{}, "content %d", i), 0o644); err != nil {
				t.Fatalf("failed to create swap file: %v", err)
			}

			if err := os.Remove(testFile); err != nil {
				t.Fatalf("failed to remove original file: %v", err)
			}

			if err := os.Rename(swapFile, testFile); err != nil {
				t.Fatalf("failed to rename swap file: %v", err)
			}
		}

		// Small delay between edits
		time.Sleep(time.Millisecond * 50)
	}

	// Wait for all pending deletes to be processed
	time.Sleep(time.Millisecond * 500)

	cancel()
	monitor.Close()

	stats := monitor.Stats(true)

	if stats.NumFilesDeleted != 0 {
		t.Errorf("expected NumFilesDeleted == 0 after multiple editor swaps, got %d", stats.NumFilesDeleted)
	}

	if stats.NumFilesCreated != 0 {
		t.Errorf("expected NumFilesCreated == 0 (swap files should be ignored), got %d", stats.NumFilesCreated)
	}
}

func TestMonitor_RealDeleteStillCounted(t *testing.T) { //nolint:cyclop
	t.Parallel()

	tempDir := t.TempDir()

	// Create files - some will be swapped, one will be really deleted
	keepFile := filepath.Join(tempDir, "keep.txt")
	deleteFile := filepath.Join(tempDir, "delete.txt")

	for _, f := range []string{keepFile, deleteFile} {
		if err := os.WriteFile(f, []byte("content"), 0o644); err != nil {
			t.Fatalf("failed to create test file %q: %v", f, err)
		}
	}

	monitor, err := files.NewMonitor(&files.MonitorOpts{
		RootPath:  tempDir,
		WatchRoot: true,
	})
	if err != nil {
		t.Fatalf("failed to start file monitor: %v", err)
	}

	go func() {
		for range monitor.Events {
			continue
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	go monitor.Run(ctx)

	time.Sleep(time.Millisecond * 100)

	// Simulate editor swap on keepFile
	swapFile := keepFile + ".swp"
	if err := os.WriteFile(swapFile, []byte("new content"), 0o644); err != nil {
		t.Fatalf("failed to create swap file: %v", err)
	}

	if err := os.Remove(keepFile); err != nil {
		t.Fatalf("failed to remove keepFile: %v", err)
	}

	if err := os.Rename(swapFile, keepFile); err != nil {
		t.Fatalf("failed to rename swap file: %v", err)
	}

	// Really delete deleteFile
	if err := os.Remove(deleteFile); err != nil {
		t.Fatalf("failed to remove deleteFile: %v", err)
	}

	// Wait for pending deletes to process
	time.Sleep(time.Millisecond * 500)

	cancel()
	monitor.Close()

	stats := monitor.Stats(true)

	// Only the real delete should be counted
	if stats.NumFilesDeleted != 1 {
		t.Errorf("expected NumFilesDeleted == 1, got %d", stats.NumFilesDeleted)
	}

	if len(stats.DeletedFiles) != 1 || stats.DeletedFiles[0] != deleteFile {
		t.Errorf("expected DeletedFiles to contain only %q, got %v", deleteFile, stats.DeletedFiles)
	}
}
