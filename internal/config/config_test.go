package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMinimalConfig(t *testing.T) {
	t.Parallel()
	content := `
[general]
db_path = "test.db"

[projects.myproject]
enabled = true
workspace = "/tmp/myproject"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "chum.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.General.DBPath != "test.db" {
		t.Errorf("unexpected db_path: %s", cfg.General.DBPath)
	}
	if cfg.General.TemporalHostPort != "localhost:7233" {
		t.Errorf("expected default temporal host, got %s", cfg.General.TemporalHostPort)
	}
	if cfg.General.TemporalNamespace != "chum-v2" {
		t.Errorf("expected default namespace, got %s", cfg.General.TemporalNamespace)
	}
	if cfg.General.TaskQueue != "chum-v2-tasks" {
		t.Errorf("expected default task queue, got %s", cfg.General.TaskQueue)
	}
	if cfg.General.MaxConcurrent != 2 {
		t.Errorf("expected default max_concurrent=2, got %d", cfg.General.MaxConcurrent)
	}
	if cfg.General.TickInterval.Duration != 2*time.Minute {
		t.Errorf("expected default tick interval 2m, got %v", cfg.General.TickInterval.Duration)
	}
}

func TestLoadPlanningDefaults(t *testing.T) {
	t.Parallel()
	content := `
[general]
[planning]
enabled = true
`
	dir := t.TempDir()
	path := filepath.Join(dir, "chum.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Planning.MaxCycles != 3 {
		t.Errorf("expected default max_cycles=3, got %d", cfg.Planning.MaxCycles)
	}
	if cfg.Planning.SignalTimeout.Duration != 30*time.Minute {
		t.Errorf("expected default signal_timeout 30m, got %v", cfg.Planning.SignalTimeout.Duration)
	}
	if cfg.Planning.SessionTimeout.Duration != 24*time.Hour {
		t.Errorf("expected default session_timeout 24h, got %v", cfg.Planning.SessionTimeout.Duration)
	}
	if cfg.Planning.MaxResearchRounds != 3 {
		t.Errorf("expected default max_research_rounds=3, got %d", cfg.Planning.MaxResearchRounds)
	}
	if cfg.Planning.PollInterval.Duration != 10*time.Second {
		t.Errorf("expected default poll_interval 10s, got %v", cfg.Planning.PollInterval.Duration)
	}
	if cfg.Planning.GroomingInterval.Duration != 5*time.Minute {
		t.Errorf("expected default grooming_interval 5m, got %v", cfg.Planning.GroomingInterval.Duration)
	}
}

func TestLoadCustomValues(t *testing.T) {
	t.Parallel()
	content := `
[general]
tick_interval = "5m"
max_concurrent = 4
temporal_host_port = "temporal:7233"
temporal_namespace = "custom-ns"
task_queue = "custom-queue"
db_path = "custom.db"

[planning]
enabled = true
max_cycles = 5
signal_timeout = "1h"
allowed_senders = ["@user:example.com"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "chum.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.General.TickInterval.Duration != 5*time.Minute {
		t.Errorf("expected 5m, got %v", cfg.General.TickInterval.Duration)
	}
	if cfg.General.MaxConcurrent != 4 {
		t.Errorf("expected 4, got %d", cfg.General.MaxConcurrent)
	}
	if cfg.General.TemporalHostPort != "temporal:7233" {
		t.Errorf("unexpected host: %s", cfg.General.TemporalHostPort)
	}
	if cfg.Planning.MaxCycles != 5 {
		t.Errorf("expected 5, got %d", cfg.Planning.MaxCycles)
	}
	if len(cfg.Planning.AllowedSenders) != 1 || cfg.Planning.AllowedSenders[0] != "@user:example.com" {
		t.Errorf("unexpected allowed_senders: %v", cfg.Planning.AllowedSenders)
	}
}

func TestLoadInvalidPath(t *testing.T) {
	t.Parallel()
	_, err := Load("/nonexistent/chum.toml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("not valid toml [[["), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestDurationUnmarshal(t *testing.T) {
	t.Parallel()
	var d Duration
	if err := d.UnmarshalText([]byte("5m30s")); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if d.Duration != 5*time.Minute+30*time.Second {
		t.Errorf("expected 5m30s, got %v", d.Duration)
	}
}

func TestDurationUnmarshalInvalid(t *testing.T) {
	t.Parallel()
	var d Duration
	if err := d.UnmarshalText([]byte("not-a-duration")); err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestDoltDefaults(t *testing.T) {
	t.Parallel()
	content := `[general]`
	dir := t.TempDir()
	path := filepath.Join(dir, "chum.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.General.DoltHost != "127.0.0.1" {
		t.Errorf("expected default dolt host, got %s", cfg.General.DoltHost)
	}
	if cfg.General.DoltPort != 3307 {
		t.Errorf("expected default dolt port 3307, got %d", cfg.General.DoltPort)
	}
	if cfg.General.DoltHealthCheckInterval.Duration != 30*time.Second {
		t.Errorf("expected default 30s, got %v", cfg.General.DoltHealthCheckInterval.Duration)
	}
}
