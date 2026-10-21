package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr   string `yaml:"listen_addr"`
	BaseURL      string `yaml:"base_url"`
	DataDir      string `yaml:"data_dir"`
	Registration string `yaml:"registration"` // "invite" | "open"
	LogLevel     string `yaml:"log_level"`
}

// Load reads the YAML file at path (optional), then applies env overrides.
// Empty path or missing file falls back to defaults.
func Load(path string) (Config, error) {
	cfg := Config{
		ListenAddr:   "127.0.0.1:8080",
		BaseURL:      "http://localhost:8080",
		DataDir:      "./data",
		Registration: "invite",
		LogLevel:     "info",
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		if err == nil {
			if err := yaml.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("parse config %s: %w", path, err)
			}
		}
	}
	env := map[string]*string{
		"STASHER_LISTEN_ADDR":  &cfg.ListenAddr,
		"STASHER_BASE_URL":     &cfg.BaseURL,
		"STASHER_DATA_DIR":     &cfg.DataDir,
		"STASHER_REGISTRATION": &cfg.Registration,
		"STASHER_LOG_LEVEL":    &cfg.LogLevel,
	}
	for k, dst := range env {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			*dst = v
		}
	}
	if cfg.Registration != "invite" && cfg.Registration != "open" {
		return cfg, fmt.Errorf("registration must be invite or open, got %q", cfg.Registration)
	}
	return cfg, nil
}
