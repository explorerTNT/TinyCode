package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigDir returns the per-user configuration directory (~/.tinycode).
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".tinycode"), nil
}

// ConfigPath returns the full path to the configuration file (~/.tinycode/config.json).
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}
