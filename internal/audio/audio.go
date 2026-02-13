package audio

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/wav"
)

//go:embed assets/*.wav
var assets embed.FS

type File struct {
	Name   string
	Format beep.Format
	Buffer *beep.Buffer
}

type Manager struct {
	mutex   sync.RWMutex
	fileMap map[string]File
}

func NewManager() (*Manager, error) {
	baseDir := "assets"

	entries, err := assets.ReadDir("assets")
	if err != nil {
		return nil, fmt.Errorf("failed to list audio assets: %w", err)
	}

	mgr := &Manager{
		fileMap: map[string]File{},
	}

	for entryIdx, entry := range entries {
		path := filepath.Join(baseDir, entry.Name())

		contents, err := assets.ReadFile(path)
		if err != nil {
			slog.Error("failed to read audio file", "path", path, "error", err)
			continue
		}

		reader := bytes.NewReader(contents)

		stream, format, err := wav.Decode(reader)
		if err != nil {
			slog.Error("failed to decode audio file as wav", "path", path, "error", err)
			continue
		}

		if entryIdx == 0 {
			if err := speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/20)); err != nil {
				return nil, fmt.Errorf("failed to initialize speaker: %w", err)
			}
		}

		buffer := beep.NewBuffer(format)
		buffer.Append(stream)

		if err := stream.Close(); err != nil {
			slog.Error("failed to close audio stream", "path", path, "error", err)
		}

		mgr.fileMap[entry.Name()] = File{
			Name:   entry.Name(),
			Format: format,
			Buffer: buffer,
		}
	}

	return mgr, nil
}

func (m *Manager) PlaySound(ctx context.Context, name string) error {
	m.mutex.RLock()

	file, ok := m.fileMap[name]
	if !ok {
		m.mutex.RUnlock()
		return fmt.Errorf("file %q not found", name)
	}

	m.mutex.RUnlock()

	done := make(chan struct{})

	stream := file.Buffer.Streamer(0, file.Buffer.Len())

	seq := beep.Seq(stream, beep.Callback(func() {
		done <- struct{}{}
	}))

	speaker.Play(seq)

	select {
	case <-ctx.Done():
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context error: %w", ctx.Err())
		}

		return nil
	case <-done:
		fmt.Printf("DONE")
		return nil
	}
}

func (m *Manager) Close() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// for name, File := range m.fileMap {
	// 	if err := File.Stream.Close(); err != nil {
	// 		slog.Error("failed to close audio file", "name", name, "error", err)
	// 	}
	// }
}
