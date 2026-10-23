package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr   string `yaml:"listen_addr"`
	BaseURL      string `yaml:"base_url"`
	DataDir      string `yaml:"data_dir"`
	Registration string `yaml:"registration"` // "invite" | "open"
	LogLevel     string `yaml:"log_level"`
	// ChromePath is an explicit chromium/chrome binary for the page
	// archiver. Empty = auto-detect (chromium, chromium-browser,
	// google-chrome on PATH); archive fetches fail with an actionable
	// error when no browser is found.
	ChromePath string `yaml:"chrome_path"`
	// BraveAPIKey is the Brave Search API key for the document finder.
	// Empty disables document search (the endpoint answers search_disabled).
	BraveAPIKey string `yaml:"brave_api_key"`
	// AI autofill ("Autofill from photos" on the add/edit form) talks to
	// an OpenAI-compatible chat-completions service. AIAPIKey is its API
	// key; empty (default) disables the feature entirely. AIModel is
	// required when the key is set — deliberately no default, a
	// wrong-vendor model name errors confusingly. AIBaseURL points at
	// OpenAI or any compatible endpoint (OpenRouter, Groq, local
	// ollama/vLLM); Load fills the OpenAI default when empty. Enabling
	// sends item photos to the configured service.
	AIAPIKey  string `yaml:"ai_api_key"`
	AIBaseURL string `yaml:"ai_base_url"`
	AIModel   string `yaml:"ai_model"`
	// TrashRetentionDays is how long trash entries (and their files)
	// survive before auto-purge. 0 or negative = keep forever.
	TrashRetentionDays int `yaml:"trash_retention_days"`
}

// Load reads the YAML file at path (optional), then applies env overrides.
// Empty path or missing file falls back to defaults.
func Load(path string) (Config, error) {
	cfg := Config{
		ListenAddr:         "127.0.0.1:8080",
		BaseURL:            "http://localhost:8080",
		DataDir:            "./data",
		Registration:       "invite",
		LogLevel:           "info",
		TrashRetentionDays: 30,
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
		"STASHER_LISTEN_ADDR":   &cfg.ListenAddr,
		"STASHER_BASE_URL":      &cfg.BaseURL,
		"STASHER_DATA_DIR":      &cfg.DataDir,
		"STASHER_REGISTRATION":  &cfg.Registration,
		"STASHER_LOG_LEVEL":     &cfg.LogLevel,
		"STASHER_CHROME_PATH":   &cfg.ChromePath,
		"STASHER_BRAVE_API_KEY": &cfg.BraveAPIKey,
		"STASHER_AI_API_KEY":    &cfg.AIAPIKey,
		"STASHER_AI_BASE_URL":   &cfg.AIBaseURL,
		"STASHER_AI_MODEL":      &cfg.AIModel,
	}
	for k, dst := range env {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			*dst = v
		}
	}
	// Default the AI base URL last: empty means stock OpenAI wherever it
	// came from (no config, or an explicit empty yaml/env value).
	if cfg.AIBaseURL == "" {
		cfg.AIBaseURL = "https://api.openai.com/v1"
	}
	if v, ok := os.LookupEnv("STASHER_TRASH_RETENTION_DAYS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("trash_retention_days must be an integer, got %q", v)
		}
		cfg.TrashRetentionDays = n
	}
	if cfg.Registration != "invite" && cfg.Registration != "open" {
		return cfg, fmt.Errorf("registration must be invite or open, got %q", cfg.Registration)
	}
	if cfg.TrashRetentionDays < 0 {
		return cfg, fmt.Errorf("trash_retention_days must be >= 0, got %d", cfg.TrashRetentionDays)
	}
	return cfg, nil
}

// PurgeAfter returns the trash purges_at timestamp for an entry trashed at
// trashedAt: trashedAt plus the retention window, or "" (never auto-purge)
// when retention is disabled. Like every timestamp in Stasher it is
// RFC3339 UTC text.
func (c Config) PurgeAfter(trashedAt time.Time) string {
	if c.TrashRetentionDays <= 0 {
		return ""
	}
	return trashedAt.AddDate(0, 0, c.TrashRetentionDays).UTC().Format(time.RFC3339)
}
