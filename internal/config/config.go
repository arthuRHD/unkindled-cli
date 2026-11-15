// Package config manages the local unkindled CLI configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultAPIURL is the control plane used when no override is configured.
const DefaultAPIURL = "https://api.unkindled.dev"

// ErrNotFound is returned by Load when no configuration file exists.
var ErrNotFound = errors.New("config not found")

// Config is the on-disk CLI configuration.
type Config struct {
	APIURL string         `json:"api_url"`
	Token  string         `json:"token"`
	User   map[string]any `json:"user,omitempty"`
}

// Dir returns the unkindled configuration directory, honoring
// XDG_CONFIG_HOME and falling back to ~/.config.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "unkindled"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	if home == "" {
		return "", errors.New("locate home directory: HOME is not set")
	}
	return filepath.Join(home, ".config", "unkindled"), nil
}

// Path returns the configuration file path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the configuration from its default location.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFile(path)
}

// LoadFile reads a configuration file from path.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if strings.TrimSpace(cfg.APIURL) == "" {
		cfg.APIURL = DefaultAPIURL
	}
	return &cfg, nil
}

// Save writes the configuration atomically with mode 0600.
func Save(cfg *Config) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	if err := SaveFile(path, cfg); err != nil {
		return "", err
	}
	return path, nil
}

// SaveFile writes cfg to path, creating parent directories with mode 0700.
func SaveFile(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure config directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create config temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure config temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure config %s: %w", path, err)
	}
	return nil
}

// Delete removes the configuration file. It reports whether a file was
// removed. It does not contact the control plane.
func Delete() (string, bool, error) {
	path, err := Path()
	if err != nil {
		return "", false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, false, nil
		}
		return path, false, fmt.Errorf("remove config %s: %w", path, err)
	}
	return path, true, nil
}
