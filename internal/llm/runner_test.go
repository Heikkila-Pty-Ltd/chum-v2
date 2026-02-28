package llm

import (
	"context"
	"testing"
)

func TestNormalizeCLIName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"claude", "claude"},
		{"Claude", "claude"},
		{"CLAUDE", "claude"},
		{"gemini", "gemini"},
		{"codex", "codex"},
		{"unknown-agent", "unknown-agent"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeCLIName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeCLIName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestProviderRegistryCoversKnownAgents(t *testing.T) {
	t.Parallel()
	knownAgents := []string{"claude", "gemini", "codex"}
	for _, agent := range knownAgents {
		if _, ok := providers[agent]; !ok {
			t.Errorf("provider registry missing agent %q", agent)
		}
	}
}

func TestBuildPlanCommandNotNil(t *testing.T) {
	t.Parallel()
	cmd := BuildPlanCommand(context.Background(), "claude", "", "/tmp")
	if cmd == nil {
		t.Fatal("BuildPlanCommand returned nil")
	}
	if cmd.Dir != "/tmp" {
		t.Errorf("expected Dir=/tmp, got %s", cmd.Dir)
	}
}

func TestBuildExecCommandNotNil(t *testing.T) {
	t.Parallel()
	cmd := BuildExecCommand(context.Background(), "gemini", "gemini-pro", "/tmp")
	if cmd == nil {
		t.Fatal("BuildExecCommand returned nil")
	}
	if cmd.Dir != "/tmp" {
		t.Errorf("expected Dir=/tmp, got %s", cmd.Dir)
	}
}

func TestCLIRunnerSatisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ Runner = CLIRunner{}
}
