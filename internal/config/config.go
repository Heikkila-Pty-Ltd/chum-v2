// Package config loads CHUM configuration from a TOML file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration wraps time.Duration for TOML unmarshalling.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", string(text), err)
	}
	return nil
}

// RateLimitRule defines per-endpoint rate limiting overrides.
type RateLimitRule struct {
	Path  string  `toml:"path"`
	Rate  float64 `toml:"rate"`
	Burst int     `toml:"burst"`
}

// RateLimit configures request rate limiting.
type RateLimit struct {
	Enabled         bool            `toml:"enabled"`
	DefaultRate     float64         `toml:"default_rate"`
	DefaultBurst    int             `toml:"default_burst"`
	Rules           []RateLimitRule `toml:"rules"`
	CleanupInterval Duration        `toml:"cleanup_interval"`
}

// Config is the top-level CHUM configuration.
type Config struct {
	General   General             `toml:"general"`
	Planning  Planning            `toml:"planning"`
	Projects  map[string]Project  `toml:"projects"`
	Providers map[string]Provider `toml:"providers"`
	Tiers     Tiers               `toml:"tiers"`
	RateLimit RateLimit           `toml:"rate_limit"`
}

// Tiers maps tier names to ordered lists of provider keys.
type Tiers struct {
	Fast     []string `toml:"fast"`
	Balanced []string `toml:"balanced"`
	Premium  []string `toml:"premium"`
}

// Planning configures the push-based planning ceremony.
type Planning struct {
	Enabled           bool     `toml:"enabled"`
	MaxCycles         int      `toml:"max_cycles"`
	SignalTimeout     Duration `toml:"signal_timeout"`
	SessionTimeout    Duration `toml:"session_timeout"`
	MaxResearchRounds int      `toml:"max_research_rounds"`
	PollInterval      Duration `toml:"poll_interval"`
	AllowedSenders    []string `toml:"allowed_senders"` // Matrix user IDs allowed to issue /plan commands (empty = allow all)
}

// General holds scheduler-level settings.
type General struct {
	TickInterval      Duration `toml:"tick_interval"`
	MaxConcurrent     int      `toml:"max_concurrent"`
	TemporalHostPort  string   `toml:"temporal_host_port"`
	TemporalNamespace string   `toml:"temporal_namespace"`
	TaskQueue         string   `toml:"task_queue"`
	DBPath            string   `toml:"db_path"`
	MatrixWebhookURL  string   `toml:"matrix_webhook_url"`
	MatrixRoomID      string   `toml:"matrix_room_id"`
	MatrixAccessToken string   `toml:"matrix_access_token"`
	MatrixHomeserver  string   `toml:"matrix_homeserver"`

	ExecTimeout  Duration `toml:"exec_timeout"`   // LLM execution timeout (default: 45m)
	ShortTimeout Duration `toml:"short_timeout"`  // short ops like push/PR (default: 2m)
	ReviewTimeout Duration `toml:"review_timeout"` // review activity timeout (default: 10m)

	DoltHealthCheckEnabled  bool     `toml:"dolt_health_check_enabled"`
	DoltHealthCheckInterval Duration `toml:"dolt_health_check_interval"`
	DoltDataDir             string   `toml:"dolt_data_dir"`
	DoltHost                string   `toml:"dolt_host"`
	DoltPort                int      `toml:"dolt_port"`

	JarvisPort int `toml:"jarvis_port"` // HTTP API port for Jarvis integration (0 = disabled)

	TracesDBPath string `toml:"traces_db_path"` // SQLite path for execution traces + perf (default: chum-traces.db)
}

// Project configures a single managed project.
type Project struct {
	Enabled   bool     `toml:"enabled"`
	Workspace string   `toml:"workspace"`
	DoDChecks []string `toml:"dod_checks"`
}

// Provider defines an LLM CLI provider.
type Provider struct {
	CLI      string `toml:"cli"`
	Model    string `toml:"model"`
	Reviewer string `toml:"reviewer"`
	Enabled  bool   `toml:"enabled"`
	Tier     string `toml:"tier"` // "fast", "balanced", or "premium" (default: "balanced")
}

// Load reads and parses a TOML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Defaults
	if cfg.General.TickInterval.Duration == 0 {
		cfg.General.TickInterval.Duration = 2 * time.Minute
	}
	if cfg.General.MaxConcurrent == 0 {
		cfg.General.MaxConcurrent = 2
	}
	if cfg.General.TemporalHostPort == "" {
		cfg.General.TemporalHostPort = "localhost:7233"
	}
	if cfg.General.TemporalNamespace == "" {
		cfg.General.TemporalNamespace = "chum-v2"
	}
	if cfg.General.TaskQueue == "" {
		cfg.General.TaskQueue = "chum-v2-tasks"
	}
	if cfg.General.DBPath == "" {
		cfg.General.DBPath = "chum.db"
	}
	if cfg.General.TracesDBPath == "" {
		cfg.General.TracesDBPath = "chum-traces.db"
	}
	if cfg.General.ExecTimeout.Duration == 0 {
		cfg.General.ExecTimeout.Duration = 45 * time.Minute
	}
	if cfg.General.ShortTimeout.Duration == 0 {
		cfg.General.ShortTimeout.Duration = 2 * time.Minute
	}
	if cfg.General.ReviewTimeout.Duration == 0 {
		cfg.General.ReviewTimeout.Duration = 10 * time.Minute
	}
	if cfg.General.DoltHealthCheckInterval.Duration == 0 {
		cfg.General.DoltHealthCheckInterval.Duration = 30 * time.Second
	}
	if cfg.General.DoltHost == "" {
		cfg.General.DoltHost = "127.0.0.1"
	}
	if cfg.General.DoltPort == 0 {
		cfg.General.DoltPort = 3307
	}
	// Resolve DoltDataDir relative to the config file's directory.
	if cfg.General.DoltDataDir != "" && !filepath.IsAbs(cfg.General.DoltDataDir) {
		configDir, _ := filepath.Abs(filepath.Dir(path))
		cfg.General.DoltDataDir = filepath.Join(configDir, cfg.General.DoltDataDir)
	}
	// Planning defaults
	if cfg.Planning.MaxCycles == 0 {
		cfg.Planning.MaxCycles = 3
	}
	if cfg.Planning.SignalTimeout.Duration == 0 {
		cfg.Planning.SignalTimeout.Duration = 30 * time.Minute
	}
	if cfg.Planning.SessionTimeout.Duration == 0 {
		cfg.Planning.SessionTimeout.Duration = 24 * time.Hour
	}
	if cfg.Planning.MaxResearchRounds == 0 {
		cfg.Planning.MaxResearchRounds = 3
	}
	if cfg.Planning.PollInterval.Duration == 0 {
		cfg.Planning.PollInterval.Duration = 10 * time.Second
	}
	// Rate limiting defaults
	if cfg.RateLimit.DefaultRate == 0 {
		cfg.RateLimit.DefaultRate = 10
	}
	if cfg.RateLimit.DefaultBurst == 0 {
		cfg.RateLimit.DefaultBurst = 20
	}
	if cfg.RateLimit.CleanupInterval.Duration == 0 {
		cfg.RateLimit.CleanupInterval.Duration = 5 * time.Minute
	}
	// Tier defaults: if no explicit [tiers] section, auto-populate from Provider.Tier fields.
	if len(cfg.Tiers.Fast) == 0 && len(cfg.Tiers.Balanced) == 0 && len(cfg.Tiers.Premium) == 0 {
		cfg.Tiers = buildTiersFromProviders(cfg.Providers)
	}

	return &cfg, nil
}

// buildTiersFromProviders auto-populates Tiers from Provider.Tier fields
// when no explicit [tiers] section is defined. Produces sorted, deterministic output.
func buildTiersFromProviders(providers map[string]Provider) Tiers {
	var t Tiers
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := providers[name]
		tier := strings.ToLower(strings.TrimSpace(p.Tier))
		if tier == "" {
			tier = "balanced"
		}
		switch tier {
		case "fast":
			t.Fast = append(t.Fast, name)
		case "balanced":
			t.Balanced = append(t.Balanced, name)
		case "premium":
			t.Premium = append(t.Premium, name)
		}
	}
	return t
}
