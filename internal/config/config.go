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
	General     General             `toml:"general"`
	Planning    Planning            `toml:"planning"`
	BeadsBridge BeadsBridge         `toml:"beads_bridge"`
	Projects    map[string]Project  `toml:"projects"`
	Providers   map[string]Provider `toml:"providers"`
	Tiers       Tiers               `toml:"tiers"`
	RateLimit   RateLimit           `toml:"rate_limit"`
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

// BeadsBridge configures Beads-first bridge behavior.
type BeadsBridge struct {
	Enabled           bool     `toml:"enabled"`
	DryRun            bool     `toml:"dry_run"`
	CanaryLabel       string   `toml:"canary_label"`
	ReconcileInterval Duration `toml:"reconcile_interval"`
	IngressPolicy     string   `toml:"ingress_policy"` // legacy | beads_first | beads_only
}

// General holds scheduler-level settings.
type General struct {
	TickInterval      Duration `toml:"tick_interval"`
	MaxConcurrent     int      `toml:"max_concurrent"`
	MaxReviewRounds   int      `toml:"max_review_rounds"` // max auto review/fix cycles before escalating (default: 5)
	TemporalHostPort  string   `toml:"temporal_host_port"`
	TemporalNamespace string   `toml:"temporal_namespace"`
	TaskQueue         string   `toml:"task_queue"`
	DBPath            string   `toml:"db_path"`
	MatrixWebhookURL  string   `toml:"matrix_webhook_url"`
	MatrixRoomID      string   `toml:"matrix_room_id"`
	MatrixAccessToken string   `toml:"matrix_access_token"`
	MatrixHomeserver  string   `toml:"matrix_homeserver"`

	ExecTimeout                Duration `toml:"exec_timeout"`                  // LLM execution timeout (default: 45m)
	ShortTimeout               Duration `toml:"short_timeout"`                 // short ops like push/PR (default: 2m)
	ReviewTimeout              Duration `toml:"review_timeout"`                // review activity timeout (default: 10m)
	RequireCrossProviderReview bool     `toml:"require_cross_provider_review"` // if true, reviewer must use a different provider than executor

	DoltHealthCheckEnabled  bool     `toml:"dolt_health_check_enabled"`
	DoltHealthCheckInterval Duration `toml:"dolt_health_check_interval"`
	DoltDataDir             string   `toml:"dolt_data_dir"`
	DoltHost                string   `toml:"dolt_host"`
	DoltPort                int      `toml:"dolt_port"`

	JarvisPort   int    `toml:"jarvis_port"`    // HTTP API port for Jarvis integration (0 = disabled)
	JarvisKBPath string `toml:"jarvis_kb_path"` // SQLite path for Jarvis knowledge base (read-only)

	TracesDBPath string `toml:"traces_db_path"` // SQLite path for execution traces + perf (default: chum-traces.db)

	Paused bool `toml:"paused"` // Legacy startup pause fallback. Runtime pause/resume is persisted in DB via global_pause.
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
	// max_review_rounds=0 means "unset" and is promoted to the default.
	if cfg.General.MaxReviewRounds == 0 {
		cfg.General.MaxReviewRounds = 5
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
	if cfg.General.MatrixHomeserver == "" {
		cfg.General.MatrixHomeserver = "https://matrix.org"
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
	// Beads bridge defaults
	if strings.TrimSpace(cfg.BeadsBridge.CanaryLabel) == "" {
		cfg.BeadsBridge.CanaryLabel = "chum-canary"
	}
	if cfg.BeadsBridge.ReconcileInterval.Duration == 0 {
		cfg.BeadsBridge.ReconcileInterval.Duration = 15 * time.Minute
	}
	if strings.TrimSpace(cfg.BeadsBridge.IngressPolicy) == "" {
		cfg.BeadsBridge.IngressPolicy = "beads_only"
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
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.General.MaxReviewRounds < 0 {
		return fmt.Errorf("invalid general.max_review_rounds %d: must be >= 0 (0 means default)", cfg.General.MaxReviewRounds)
	}

	switch strings.ToLower(strings.TrimSpace(cfg.BeadsBridge.IngressPolicy)) {
	case "legacy", "beads_first", "beads-only", "beads_only":
		// Normalize aliases to stable internal values.
		if strings.EqualFold(cfg.BeadsBridge.IngressPolicy, "beads-only") {
			cfg.BeadsBridge.IngressPolicy = "beads_only"
		} else {
			cfg.BeadsBridge.IngressPolicy = strings.ToLower(strings.TrimSpace(cfg.BeadsBridge.IngressPolicy))
		}
	default:
		return fmt.Errorf("invalid beads_bridge.ingress_policy %q (allowed: legacy, beads_first, beads_only)", cfg.BeadsBridge.IngressPolicy)
	}

	cfg.BeadsBridge.CanaryLabel = strings.TrimSpace(cfg.BeadsBridge.CanaryLabel)
	if cfg.BeadsBridge.CanaryLabel == "" {
		return fmt.Errorf("invalid beads_bridge.canary_label: must be non-empty")
	}
	if strings.ContainsAny(cfg.BeadsBridge.CanaryLabel, " \t\r\n") {
		return fmt.Errorf("invalid beads_bridge.canary_label %q: must not contain whitespace", cfg.BeadsBridge.CanaryLabel)
	}
	if cfg.BeadsBridge.ReconcileInterval.Duration <= 0 {
		return fmt.Errorf("invalid beads_bridge.reconcile_interval %q: must be > 0", cfg.BeadsBridge.ReconcileInterval.Duration)
	}

	if cfg.Planning.PollInterval.Duration <= 0 {
		return fmt.Errorf("invalid planning.poll_interval %q: must be > 0", cfg.Planning.PollInterval.Duration)
	}

	// Planning allowed_senders normalization.
	if len(cfg.Planning.AllowedSenders) > 0 {
		var filtered []string
		for _, s := range cfg.Planning.AllowedSenders {
			trimmed := strings.TrimSpace(s)
			if trimmed != "" {
				filtered = append(filtered, trimmed)
			}
		}
		cfg.Planning.AllowedSenders = filtered
	}

	// Matrix configuration validation and normalization.
	cfg.General.MatrixHomeserver = strings.TrimSuffix(strings.TrimSpace(cfg.General.MatrixHomeserver), "/")
	cfg.General.MatrixAccessToken = strings.TrimSpace(cfg.General.MatrixAccessToken)
	cfg.General.MatrixRoomID = strings.TrimSpace(cfg.General.MatrixRoomID)
	cfg.General.MatrixWebhookURL = strings.TrimSpace(cfg.General.MatrixWebhookURL)

	if cfg.General.MatrixAccessToken != "" || cfg.General.MatrixRoomID != "" {
		if cfg.General.MatrixHomeserver == "" {
			return fmt.Errorf("matrix_homeserver is required when matrix configuration is provided")
		}
		if cfg.General.MatrixAccessToken == "" {
			return fmt.Errorf("matrix_access_token is required when matrix configuration is provided")
		}
		if cfg.General.MatrixRoomID == "" {
			return fmt.Errorf("matrix_room_id is required when matrix configuration is provided")
		}
	}

	return nil
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
