package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q, want 127.0.0.1:8080", cfg.ListenAddr)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want ./data", cfg.DataDir)
	}
	if cfg.Registration != "invite" {
		t.Errorf("Registration = %q, want invite", cfg.Registration)
	}
	if cfg.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestLoadYamlAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stasher.yaml")
	os.WriteFile(path, []byte("listen_addr: 0.0.0.0:9000\ndata_dir: /srv/data\n"), 0o644)

	t.Setenv("STASHER_DATA_DIR", "/other/data")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "0.0.0.0:9000" {
		t.Errorf("ListenAddr = %q, want from YAML", cfg.ListenAddr)
	}
	if cfg.DataDir != "/other/data" {
		t.Errorf("DataDir = %q, want env override", cfg.DataDir)
	}
	if cfg.Registration != "invite" {
		t.Errorf("Registration = %q, want default", cfg.Registration)
	}
}

func TestLoadTrashRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stasher.yaml")

	// YAML sets, env overrides
	os.WriteFile(path, []byte("trash_retention_days: 7\n"), 0o644)
	t.Setenv("STASHER_TRASH_RETENTION_DAYS", "14")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrashRetentionDays != 14 {
		t.Errorf("TrashRetentionDays = %d, want env override 14", cfg.TrashRetentionDays)
	}

	// zero disables auto-purge (valid)
	t.Setenv("STASHER_TRASH_RETENTION_DAYS", "0")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrashRetentionDays != 0 {
		t.Errorf("TrashRetentionDays = %d, want 0", cfg.TrashRetentionDays)
	}

	// negative rejected
	t.Setenv("STASHER_TRASH_RETENTION_DAYS", "-1")
	if _, err = Load(path); err == nil {
		t.Error("negative trash_retention_days accepted, want error")
	}

	// non-integer rejected
	t.Setenv("STASHER_TRASH_RETENTION_DAYS", "soon")
	if _, err = Load(path); err == nil {
		t.Error("non-integer trash_retention_days accepted, want error")
	}
}

func TestLoadChromePath(t *testing.T) {
	// default empty (auto-detect)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChromePath != "" {
		t.Errorf("ChromePath = %q, want empty default", cfg.ChromePath)
	}

	// YAML sets, env overrides
	dir := t.TempDir()
	path := filepath.Join(dir, "stasher.yaml")
	os.WriteFile(path, []byte("chrome_path: /usr/bin/chromium\n"), 0o644)
	t.Setenv("STASHER_CHROME_PATH", "/opt/chrome")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChromePath != "/opt/chrome" {
		t.Errorf("ChromePath = %q, want env override", cfg.ChromePath)
	}

	t.Setenv("STASHER_CHROME_PATH", "")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChromePath != "/usr/bin/chromium" {
		t.Errorf("ChromePath = %q, want YAML value", cfg.ChromePath)
	}
}

func TestLoadBraveAPIKey(t *testing.T) {
	// default empty (search disabled)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BraveAPIKey != "" {
		t.Errorf("BraveAPIKey = %q, want empty default", cfg.BraveAPIKey)
	}

	// YAML sets, env overrides
	dir := t.TempDir()
	path := filepath.Join(dir, "stasher.yaml")
	os.WriteFile(path, []byte("brave_api_key: yaml-key\n"), 0o644)
	t.Setenv("STASHER_BRAVE_API_KEY", "env-key")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BraveAPIKey != "env-key" {
		t.Errorf("BraveAPIKey = %q, want env override", cfg.BraveAPIKey)
	}

	t.Setenv("STASHER_BRAVE_API_KEY", "")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BraveAPIKey != "yaml-key" {
		t.Errorf("BraveAPIKey = %q, want YAML value", cfg.BraveAPIKey)
	}
}

func TestPurgeAfter(t *testing.T) {
	trashed := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	cfg := Config{TrashRetentionDays: 30}
	if got := cfg.PurgeAfter(trashed); got != "2026-11-20T12:00:00Z" {
		t.Errorf("PurgeAfter(30) = %q", got)
	}
	cfg.TrashRetentionDays = 0
	if got := cfg.PurgeAfter(trashed); got != "" {
		t.Errorf("PurgeAfter(0) = %q, want \"\" (never purge)", got)
	}
}
