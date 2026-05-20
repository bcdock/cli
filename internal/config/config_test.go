package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bcdock/cli/internal/config"
)

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)

	cfg := &config.Config{ApiUrl: "http://localhost:5001", DefaultOutput: "json"}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.ApiUrl != cfg.ApiUrl || got.DefaultOutput != cfg.DefaultOutput {
		t.Errorf("got %+v, want %+v", got, cfg)
	}

	// Credentials file must be mode 0600
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config.json perm = %o, want 0600", perm)
	}
}

func TestSaveAndLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BCDOCK_CONFIG_DIR", dir)

	creds := &config.Credentials{Token: "bdk_test123"}
	if err := config.SaveCredentials(creds); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	got, err := config.LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if got.Token != creds.Token {
		t.Errorf("got %+v, want %+v", got, creds)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoadCredentials_MissingFile(t *testing.T) {
	t.Setenv("BCDOCK_CONFIG_DIR", t.TempDir())

	creds, err := config.LoadCredentials()
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
}
