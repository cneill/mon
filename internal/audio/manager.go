package audio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/vorbis"
	"github.com/gopxl/beep/v2/wav"
)

type Manager struct {
	mutex    sync.RWMutex
	soundMap map[string]Sound
	// TODO: hookMap
}

func NewManager() (*Manager, error) {
	mgr := &Manager{
		soundMap: map[string]Sound{},
	}

	if err := mgr.loadBuiltins(); err != nil {
		return nil, fmt.Errorf("failed to load built-in sounds")
	}

	return mgr, nil
}

func (m *Manager) AddSound(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	name := filepath.Base(path)

	stream, format, err := m.getStream(name, file)
	if err != nil {
		return fmt.Errorf("failed to get stream: %w", err)
	}

	if err := m.addSound(name, stream, format); err != nil {
		return err
	}

	return nil
}

func (m *Manager) PlaySound(ctx context.Context, name string) error {
	m.mutex.RLock()

	sound, ok := m.soundMap[name]
	if !ok {
		m.mutex.RUnlock()
		return fmt.Errorf("sound %q not found", name)
	}

	m.mutex.RUnlock()

	// TODO: beep.Ctrl to kill w/ ctx

	done := make(chan struct{})
	stream := sound.Buffer.Streamer(0, sound.Buffer.Len())
	seq := beep.Seq(stream, beep.Callback(func() {
		done <- struct{}{}
	}))

	// speaker.PlayAndWait(stream)

	speaker.Play(seq)

	select {
	case <-ctx.Done():
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context error: %w", ctx.Err())
		}

		return nil
	case <-done:
		return nil
	}
}

func (m *Manager) Close() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
}

func (m *Manager) loadBuiltins() error {
	baseDir := "assets"

	entries, err := builtinAssets.ReadDir("assets")
	if err != nil {
		return fmt.Errorf("failed to list audio assets: %w", err)
	}

	for entryIdx, entry := range entries {
		path := filepath.Join(baseDir, entry.Name())

		contents, err := builtinAssets.ReadFile(path)
		if err != nil {
			slog.Error("failed to read builtin audio file", "path", path, "error", err)
			continue
		}

		reader := io.NopCloser(bytes.NewReader(contents))

		stream, format, err := m.getStream(entry.Name(), reader)
		if err != nil {
			slog.Error("failed to get stream", "path", path, "error", err)
			continue
		}

		if entryIdx == 0 {
			if err := speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/20)); err != nil {
				return fmt.Errorf("failed to initialize speaker: %w", err)
			}
		}

		if err := m.addSound(entry.Name(), stream, format); err != nil {
			slog.Error("failed to add built-in sound", "name", entry.Name(), "error", err)
		}
	}

	return nil
}

func (m *Manager) getStream(name string, reader io.ReadCloser) (beep.StreamSeekCloser, beep.Format, error) {
	var (
		stream beep.StreamSeekCloser
		format beep.Format
		err    error
	)

	extension := filepath.Ext(name)

	switch extension {
	case ".mp3":
		stream, format, err = mp3.Decode(reader)
		if err != nil {
			return stream, format, fmt.Errorf("failed to decode file as mp3: %w", err)
		}
	case ".ogg":
		stream, format, err = vorbis.Decode(reader)
		if err != nil {
			return stream, format, fmt.Errorf("failed to decode file as mp3: %w", err)
		}
	case ".wav":
		stream, format, err = wav.Decode(reader)
		if err != nil {
			return stream, format, fmt.Errorf("failed to decode file as wav: %w", err)
		}
	default:
		return stream, format, fmt.Errorf("unknown file format/extension: %s", extension)
	}

	return stream, format, nil
}

func (m *Manager) addSound(name string, stream beep.StreamSeekCloser, format beep.Format) error {
	buffer := beep.NewBuffer(format)
	buffer.Append(stream)

	if err := stream.Close(); err != nil {
		return fmt.Errorf("failed to close audio stream after buffering: %w", err)
	}

	m.mutex.Lock()
	m.soundMap[name] = Sound{
		Name:   name,
		Format: format,
		Buffer: buffer,
	}
	m.mutex.Unlock()

	return nil
}
