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

func TestDirectTaskIngressBlocked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{
			name: "nil config",
			cfg:  nil,
			want: false,
		},
		{
			name: "bridge disabled",
			cfg: &config.Config{
				BeadsBridge: config.BeadsBridge{Enabled: false, IngressPolicy: "beads_only"},
			},
			want: false,
		},
		{
			name: "legacy policy",
			cfg: &config.Config{
				BeadsBridge: config.BeadsBridge{Enabled: true, IngressPolicy: "legacy"},
			},
			want: false,
		},
		{
			name: "beads only policy",
			cfg: &config.Config{
				BeadsBridge: config.BeadsBridge{Enabled: true, IngressPolicy: "beads_only"},
			},
			want: true,
		},
		{
			name: "beads first policy",
			cfg: &config.Config{
				BeadsBridge: config.BeadsBridge{Enabled: true, IngressPolicy: "beads_first"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := directTaskIngressBlocked(tt.cfg)
			if got != tt.want {
				t.Fatalf("directTaskIngressBlocked() = %t, want %t", got, tt.want)
			}
		})
	}
}
