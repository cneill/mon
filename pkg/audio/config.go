package audio

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Hooks map[EventType]string `json:"hooks"`
}

func DefaultConfig() *Config {
	return &Config{
		Hooks: map[EventType]string{},
	}
}

func (c *Config) OK() error {
	if c.Hooks == nil {
		return nil
	}

	errors := []string{}

	for eventType, path := range c.Hooks {
		if !ValidEventType(eventType) {
			errors = append(errors, fmt.Sprintf("unknown event type: %s", eventType))
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
