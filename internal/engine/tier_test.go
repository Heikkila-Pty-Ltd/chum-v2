package engine

import (
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
)

func TestTierForEstimate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		estimate int
		want     string
	}{
		{"zero_defaults_to_balanced", 0, "balanced"},
		{"negative_defaults_to_balanced", -5, "balanced"},
		{"1min_fast", 1, "fast"},
		{"5min_fast_boundary", 5, "fast"},
		{"6min_balanced", 6, "balanced"},
		{"10min_balanced_boundary", 10, "balanced"},
		{"11min_premium", 11, "premium"},
		{"15min_premium", 15, "premium"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := TierForEstimate(tt.estimate); got != tt.want {
				t.Errorf("TierForEstimate(%d) = %q, want %q", tt.estimate, got, tt.want)
			}
		})
	}
}

func TestEscChain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		start string
		want  []string
	}{
		{"fast", []string{"fast", "balanced", "premium"}},
		{"balanced", []string{"balanced", "premium"}},
		{"premium", []string{"premium"}},
		{"unknown", []string{"fast", "balanced", "premium"}},
	}
	for _, tt := range tests {
		t.Run(tt.start, func(t *testing.T) {
			t.Parallel()
			got := escChain(tt.start)
			if len(got) != len(tt.want) {
				t.Fatalf("escChain(%q) = %v, want %v", tt.start, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("escChain(%q)[%d] = %q, want %q", tt.start, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestPickProviderForTier_HappyPath(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{
			"gemini": {CLI: "gemini", Model: "gemini-2.5-flash", Enabled: true},
		},
		Tiers: config.Tiers{Fast: []string{"gemini"}},
	}
	cli, model, name := PickProviderForTier(cfg, "fast")
	if cli != "gemini" || model != "gemini-2.5-flash" || name != "gemini" {
		t.Errorf("got (%q, %q, %q), want (gemini, gemini-2.5-flash, gemini)", cli, model, name)
	}
}

func TestPickProviderForTier_SkipsDisabled(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{
			"gemini": {CLI: "gemini", Model: "flash", Enabled: false},
			"codex":  {CLI: "codex", Model: "gpt-5", Enabled: true},
		},
		Tiers: config.Tiers{Fast: []string{"gemini", "codex"}},
	}
	cli, model, name := PickProviderForTier(cfg, "fast")
	if cli != "codex" || model != "gpt-5" || name != "codex" {
		t.Errorf("got (%q, %q, %q), want (codex, gpt-5, codex)", cli, model, name)
	}
}

func TestPickProviderForTier_EmptyTier(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{
			"claude": {CLI: "claude", Model: "sonnet", Enabled: true},
		},
		Tiers: config.Tiers{},
	}
	cli, model, name := PickProviderForTier(cfg, "fast")
	if cli != "" || model != "" || name != "" {
		t.Errorf("expected empty result for empty tier, got (%q, %q, %q)", cli, model, name)
	}
}

func TestPickProviderForTier_NilConfig(t *testing.T) {
	t.Parallel()
	cli, model, name := PickProviderForTier(nil, "fast")
	if cli != "" || model != "" || name != "" {
		t.Errorf("expected empty result for nil config, got (%q, %q, %q)", cli, model, name)
	}
}

func TestPickProviderForTier_MissingProviderKey(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{},
		Tiers:     config.Tiers{Fast: []string{"nonexistent"}},
	}
	cli, model, name := PickProviderForTier(cfg, "fast")
	if cli != "" || model != "" || name != "" {
		t.Errorf("expected empty for missing provider key, got (%q, %q, %q)", cli, model, name)
	}
}

func TestPickProvider_StartsAtFast(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{
			"gemini": {CLI: "gemini", Model: "flash", Enabled: true},
			"claude": {CLI: "claude", Model: "sonnet", Enabled: true},
		},
		Tiers: config.Tiers{
			Fast:     []string{"gemini"},
			Balanced: []string{"claude"},
		},
	}
	cli, model, tier := PickProvider(cfg, "fast")
	if cli != "gemini" || model != "flash" || tier != "fast" {
		t.Errorf("got (%q, %q, %q), want (gemini, flash, fast)", cli, model, tier)
	}
}

func TestPickProvider_EscalatesToBalanced(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{
			"claude": {CLI: "claude", Model: "sonnet", Enabled: true},
		},
		Tiers: config.Tiers{
			Fast:     []string{},
			Balanced: []string{"claude"},
		},
	}
	cli, model, tier := PickProvider(cfg, "fast")
	if cli != "claude" || model != "sonnet" || tier != "balanced" {
		t.Errorf("got (%q, %q, %q), want (claude, sonnet, balanced)", cli, model, tier)
	}
}

func TestPickProvider_FallbackWhenAllEmpty(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{},
		Tiers:     config.Tiers{},
	}
	cli, model, tier := PickProvider(cfg, "fast")
	if cli != "" || model != "" || tier != "" {
		t.Errorf("got (%q, %q, %q), want (\"\", \"\", \"\")", cli, model, tier)
	}
}

func TestPickProvider_PremiumFallsBackToEnabledLowerTier(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{
			"gemini": {CLI: "gemini", Model: "flash", Enabled: true, Tier: "fast"},
		},
		Tiers: config.Tiers{
			Fast:     []string{"gemini"},
			Balanced: []string{"gemini"},
			Premium:  []string{},
		},
	}
	cli, model, tier := PickProvider(cfg, "premium")
	if cli != "gemini" || model != "flash" || tier != "fast" {
		t.Errorf("got (%q, %q, %q), want (gemini, flash, fast)", cli, model, tier)
	}
}

func TestPickProvider_StartsAtBalancedSkipsFast(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Providers: map[string]config.Provider{
			"gemini": {CLI: "gemini", Model: "flash", Enabled: true},
			"claude": {CLI: "claude", Model: "sonnet", Enabled: true},
		},
		Tiers: config.Tiers{
			Fast:     []string{"gemini"},
			Balanced: []string{"claude"},
		},
	}
	cli, _, tier := PickProvider(cfg, "balanced")
	if cli != "claude" || tier != "balanced" {
		t.Errorf("starting at balanced should skip fast; got (%q, %q)", cli, tier)
	}
}
