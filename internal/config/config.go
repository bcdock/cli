package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	ApiUrl        string `json:"apiUrl"`
	DefaultOutput string `json:"defaultOutput"`
}

type Credentials struct {
	Token string `json:"token"`
}

// ConfigDir returns the BCDock config directory.
// Respects $BCDOCK_CONFIG_DIR; falls back to ~/.bcdock.
func ConfigDir() string {
	if d := os.Getenv("BCDOCK_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".bcdock"
	}
	return filepath.Join(home, ".bcdock")
}

func LoadConfig() (*Config, error) {
	data, err := os.ReadFile(filepath.Join(ConfigDir(), "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)
}

func LoadCredentials() (*Credentials, error) {
	data, err := os.ReadFile(filepath.Join(ConfigDir(), "credentials.json"))
	if errors.Is(err, os.ErrNotExist) {
		return &Credentials{}, nil
	}
	if err != nil {
		return nil, err
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

func SaveCredentials(creds *Credentials) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0600)
}
