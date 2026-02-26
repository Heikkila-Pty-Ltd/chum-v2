// Package config loads CHUM configuration from a TOML file.
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration wraps time.Duration for TOML unmarshalling.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

// Config is the top-level CHUM configuration.
type Config struct {
	General   General             `toml:"general"`
	Projects  map[string]Project  `toml:"projects"`
	Providers map[string]Provider `toml:"providers"`
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
	return &cfg, nil
}
