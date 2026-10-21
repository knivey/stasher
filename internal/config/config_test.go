package config

import (
	"os"
	"path/filepath"
	"testing"
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
