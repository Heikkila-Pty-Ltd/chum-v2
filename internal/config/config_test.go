package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_DefaultValues(t *testing.T) {
	// Create a temporary TOML file without rate_limit section
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	content := `
[general]
tick_interval = "1m"
max_concurrent = 5

[providers]
[providers.claude]
cli = "claude"
model = "claude-3-5-sonnet-20241022"
enabled = true
`

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Check rate limit defaults
	if cfg.RateLimit.DefaultRate != 10.0 {
		t.Errorf("Expected DefaultRate = 10.0, got %f", cfg.RateLimit.DefaultRate)
	}
	if cfg.RateLimit.DefaultBurst != 20 {
		t.Errorf("Expected DefaultBurst = 20, got %d", cfg.RateLimit.DefaultBurst)
	}
	if cfg.RateLimit.CleanupInterval.Duration != 5*time.Minute {
		t.Errorf("Expected CleanupInterval = 5m, got %v", cfg.RateLimit.CleanupInterval.Duration)
	}
	if cfg.RateLimit.Enabled {
		t.Error("Expected Enabled = false (zero value), got true")
	}
	if len(cfg.RateLimit.Rules) != 0 {
		t.Errorf("Expected empty Rules, got %d rules", len(cfg.RateLimit.Rules))
	}
}

func TestLoad_WithRateLimitConfig(t *testing.T) {
	// Create a temporary TOML file with rate_limit section and rules
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	content := `
[general]
tick_interval = "1m"
max_concurrent = 5

[rate_limit]
enabled = true
default_rate = 15.5
default_burst = 30
cleanup_interval = "10m"

[[rate_limit.rules]]
path = "/api/v1/heavy"
rate = 2.0
burst = 5

[[rate_limit.rules]]
path = "/api/v1/upload"
rate = 1.0
burst = 2

[providers]
[providers.claude]
cli = "claude"
model = "claude-3-5-sonnet-20241022"
enabled = true
`

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Check rate limit configuration
	if !cfg.RateLimit.Enabled {
		t.Error("Expected Enabled = true, got false")
	}
	if cfg.RateLimit.DefaultRate != 15.5 {
		t.Errorf("Expected DefaultRate = 15.5, got %f", cfg.RateLimit.DefaultRate)
	}
	if cfg.RateLimit.DefaultBurst != 30 {
		t.Errorf("Expected DefaultBurst = 30, got %d", cfg.RateLimit.DefaultBurst)
	}
	if cfg.RateLimit.CleanupInterval.Duration != 10*time.Minute {
		t.Errorf("Expected CleanupInterval = 10m, got %v", cfg.RateLimit.CleanupInterval.Duration)
	}

	// Check rules
	if len(cfg.RateLimit.Rules) != 2 {
		t.Fatalf("Expected 2 rules, got %d", len(cfg.RateLimit.Rules))
	}

	// Check first rule
	rule1 := cfg.RateLimit.Rules[0]
	if rule1.Path != "/api/v1/heavy" {
		t.Errorf("Expected rule1.Path = '/api/v1/heavy', got '%s'", rule1.Path)
	}
	if rule1.Rate != 2.0 {
		t.Errorf("Expected rule1.Rate = 2.0, got %f", rule1.Rate)
	}
	if rule1.Burst != 5 {
		t.Errorf("Expected rule1.Burst = 5, got %d", rule1.Burst)
	}

	// Check second rule
	rule2 := cfg.RateLimit.Rules[1]
	if rule2.Path != "/api/v1/upload" {
		t.Errorf("Expected rule2.Path = '/api/v1/upload', got '%s'", rule2.Path)
	}
	if rule2.Rate != 1.0 {
		t.Errorf("Expected rule2.Rate = 1.0, got %f", rule2.Rate)
	}
	if rule2.Burst != 2 {
		t.Errorf("Expected rule2.Burst = 2, got %d", rule2.Burst)
	}
}

func TestLoad_PartialRateLimitConfig(t *testing.T) {
	// Create a temporary TOML file with partial rate_limit section (only some fields)
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	content := `
[general]
tick_interval = "1m"

[rate_limit]
enabled = true
default_rate = 25.0
# default_burst and cleanup_interval omitted - should get defaults
`

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Check rate limit configuration - should have custom rate but default burst/cleanup
	if !cfg.RateLimit.Enabled {
		t.Error("Expected Enabled = true, got false")
	}
	if cfg.RateLimit.DefaultRate != 25.0 {
		t.Errorf("Expected DefaultRate = 25.0, got %f", cfg.RateLimit.DefaultRate)
	}
	if cfg.RateLimit.DefaultBurst != 20 { // Should get default
		t.Errorf("Expected DefaultBurst = 20 (default), got %d", cfg.RateLimit.DefaultBurst)
	}
	if cfg.RateLimit.CleanupInterval.Duration != 5*time.Minute { // Should get default
		t.Errorf("Expected CleanupInterval = 5m (default), got %v", cfg.RateLimit.CleanupInterval.Duration)
	}
}