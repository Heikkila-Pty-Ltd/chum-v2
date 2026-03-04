package main

import (
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
)

func TestDefaultProject_DeterministicSortedSelection(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Projects: map[string]config.Project{
			"zeta":  {Enabled: true},
			"alpha": {Enabled: true},
			"beta":  {Enabled: true},
		},
	}

	got := defaultProject(cfg)
	if got != "alpha" {
		t.Fatalf("defaultProject() = %q, want %q", got, "alpha")
	}
}

func TestDefaultProject_SkipsDisabled(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Projects: map[string]config.Project{
			"alpha": {Enabled: false},
			"beta":  {Enabled: true},
		},
	}

	got := defaultProject(cfg)
	if got != "beta" {
		t.Fatalf("defaultProject() = %q, want %q", got, "beta")
	}
}

func TestDefaultProject_EmptyOrNil(t *testing.T) {
	t.Parallel()

	if got := defaultProject(nil); got != "" {
		t.Fatalf("defaultProject(nil) = %q, want empty", got)
	}
	if got := defaultProject(&config.Config{}); got != "" {
		t.Fatalf("defaultProject(empty) = %q, want empty", got)
	}
}
