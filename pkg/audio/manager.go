package audio

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/vorbis"
	"github.com/gopxl/beep/v2/wav"
)

//go:embed assets/*.wav
var builtinAssets embed.FS

var ErrSoundNotFound = errors.New("sound not found")

type ManagerOpts struct {
	HookMap map[string]string
}

func (m *ManagerOpts) OK() error {
	if m.HookMap == nil {
		return nil
	}

	errors := []string{}

	for eventType, path := range m.HookMap {
		if !ValidEventType(EventType(eventType)) {
			errors = append(errors, "unknown event type: "+eventType)
		}

		stat, err := os.Stat(path)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to stat audio file %s: %v", path, err))
			continue
		}

		if !stat.Mode().IsRegular() {
			errors = append(errors, fmt.Sprintf("file %s is not a regular file", path))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("options error: %s", strings.Join(errors, "; "))
	}

	return nil
}

type Manager struct {
	soundMutex sync.RWMutex
	soundMap   map[string]*Sound

	hookMutex sync.RWMutex
	hookMap   map[EventType]string // value = sound name
}

func NewManager(opts *ManagerOpts) (*Manager, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("invalid audio manager options: %w", err)
	}

	mgr := &Manager{
		soundMap: map[string]*Sound{},
		hookMap:  map[EventType]string{},
	}

	if err := mgr.loadBuiltins(); err != nil {
		return nil, fmt.Errorf("failed to load built-in sounds")
	}

	return mgr, nil
}

// AddSound takes the path to a sound and stores it for use by the Manager based on event hooks.
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

// AddEventHook takes the 'name' of a sound (the filename, not the full path), and configures Manager to play it
// whenever an event of 'eventType' is received.
func (m *Manager) AddEventHook(name string, eventType EventType) error {
	sound, err := m.GetSound(name)
	if err != nil {
		return err
	} else if sound == nil {
		return fmt.Errorf("%w: %s", ErrSoundNotFound, name)
	}

	m.hookMutex.Lock()
	defer m.hookMutex.Unlock()

	m.hookMap[eventType] = name

	return nil
}

func (m *Manager) GetSound(name string) (*Sound, error) {
	m.soundMutex.RLock()
	defer m.soundMutex.RUnlock()

	sound, ok := m.soundMap[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSoundNotFound, name)
	}

	return sound, nil
}

func (m *Manager) PlaySound(ctx context.Context, name string) error {
	sound, err := m.GetSound(name)
	if err != nil {
		return err
	}

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
	m.soundMutex.Lock()
	defer m.soundMutex.Unlock()
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
			slog.Error("Failed to read builtin audio file", "path", path, "error", err)
			continue
		}

		reader := io.NopCloser(bytes.NewReader(contents))

		stream, format, err := m.getStream(entry.Name(), reader)
		if err != nil {
			slog.Error("Failed to get stream", "path", path, "error", err)
			continue
		}

		if entryIdx == 0 {
			if err := speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/20)); err != nil {
				return fmt.Errorf("failed to initialize speaker: %w", err)
			}
		}

		if err := m.addSound(entry.Name(), stream, format); err != nil {
			slog.Error("Failed to add built-in sound", "name", entry.Name(), "error", err)
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

	m.soundMutex.Lock()
	m.soundMap[name] = &Sound{
		Name:   name,
		Format: format,
		Buffer: buffer,
	}
	m.soundMutex.Unlock()

	return nil
}
