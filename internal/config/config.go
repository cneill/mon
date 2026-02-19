package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cneill/mon/pkg/audio"
)

type Config struct {
	Audio *audio.Config `json:"audio"`
}

func (c *Config) OK() error {
	if c.Audio != nil {
		if err := c.Audio.OK(); err != nil {
			return fmt.Errorf("error with audio config: %w", err)
		}
	}

	return nil
}

// Load reads and parses the configuration file. If no path is supplied, or if the provided path doesn't exist and there
// isn't a config in the default path, write one.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath()
		if path == "" {
			return nil, fmt.Errorf("could not determine proper default config path")
		}
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("config file %q does not exist", path)
	} else if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Config{}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := cfg.OK(); err != nil {
		return nil, fmt.Errorf("error with config file: %w", err)
	}

	slog.Debug("Loaded config file", "path", path)

	return cfg, nil
}

// DefaultConfigDir returns $HOME/.config/aimon
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Error("Failed to locate user home directory", "error", err)
		return ""
	}

	return filepath.Join(home, ".config", "mon")
}

// DefaultConfigPath returns the default configuration file path ($HOME/.config/aimon/config.json)
func DefaultConfigPath() string {
	dir := DefaultConfigDir()
	if dir == "" {
		return ""
	}

	return filepath.Join(dir, "config.json")
}
