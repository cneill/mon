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
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/vorbis"
	"github.com/gopxl/beep/v2/wav"
	"golang.org/x/time/rate"
)

//go:embed assets/*
var builtinAssets embed.FS

var ErrSoundNotFound = errors.New("sound not found")

//nolint:gochecknoglobals
var speakerSampleRate beep.SampleRate

type Manager struct {
	soundMutex sync.RWMutex
	soundMap   map[string]*Sound

	hookMutex sync.RWMutex
	hookMap   map[EventType]string // value = sound name

	eventChan chan Event
	limiter   *rate.Limiter
}

func NewManager(cfg *Config) (*Manager, error) {
	if cfg != nil {
		if err := cfg.OK(); err != nil {
			return nil, fmt.Errorf("invalid audio config: %w", err)
		}
	}

	mgr := &Manager{
		soundMap:  map[string]*Sound{},
		hookMap:   map[EventType]string{},
		eventChan: make(chan Event),
		limiter:   rate.NewLimiter(5, 1),
	}

	if err := mgr.loadBuiltins(); err != nil {
		return nil, fmt.Errorf("failed to load built-in sounds: %w", err)
	}

	mgr.applyDefaults()

	// Apply user overrides from config
	if cfg != nil {
		for eventType, path := range cfg.Hooks {
			if path == "" {
				continue
			}

			if err := mgr.AddSound(path); err != nil {
				return nil, fmt.Errorf("failed to add sound %q: %w", path, err)
			}

			if err := mgr.AddEventHook(filepath.Base(path), eventType); err != nil {
				return nil, fmt.Errorf("failed to add event hook for %q: %w", eventType, err)
			}
		}
	}

	if sound, ok := mgr.hookMap[EventInit]; ok {
		go func() {
			if err := mgr.PlaySound(context.Background(), sound); err != nil {
				slog.Error("Failed to play init sound", "error", err)
			}
		}()
	}

	return mgr, nil
}

func (m *Manager) Run(ctx context.Context) {
	go m.eventLoop(ctx)
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

			speakerSampleRate = format.SampleRate
		}

		if err := m.addSound(entry.Name(), stream, format); err != nil {
			slog.Error("Failed to add built-in sound", "name", entry.Name(), "error", err)
		}
	}

	return nil
}

func (m *Manager) applyDefaults() {
	m.hookMutex.Lock()
	defer m.hookMutex.Unlock()

	// Apply default hooks
	m.hookMap[EventCommitCreate] = "commit_create.mp3"
	m.hookMap[EventFileCreate] = "file_create.mp3"
	m.hookMap[EventFileRemove] = "file_remove.mp3"
	m.hookMap[EventFileWrite] = "file_write.mp3"
	m.hookMap[EventPackageCreate] = "package_create.mp3"
	m.hookMap[EventPackageRemove] = "package_remove.mp3"
	m.hookMap[EventPackageUpgrade] = "package_upgrade.mp3"
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
	resampledStream := beep.Resample(4, format.SampleRate, speakerSampleRate, stream)

	buffer.Append(resampledStream)

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
