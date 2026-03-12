package engine

import (
	"sort"
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
)

// TierOrder defines the escalation chain from cheapest to most capable.
var TierOrder = []string{"fast", "balanced", "premium"}

// Estimate thresholds for tier assignment.
const (
	// Tasks estimated at or below this are "fast" (simple, cheap model).
	fastMaxMinutes = 5
	// Tasks estimated at or below this are "balanced" (moderate complexity).
	balancedMaxMinutes = 10
	// Tasks above balancedMaxMinutes are "premium" (complex, best model).
)

// TierForEstimate maps a task's estimated difficulty (in minutes) to a tier.
//
//	0 (unset) → balanced (safe default)
//	1-5 min   → fast
//	6-10 min  → balanced
//	11-15 min → premium
func TierForEstimate(estimateMinutes int) string {
	switch {
	case estimateMinutes <= 0:
		return "balanced"
	case estimateMinutes <= fastMaxMinutes:
		return "fast"
	case estimateMinutes <= balancedMaxMinutes:
		return "balanced"
	default:
		return "premium"
	}
}

// PickProviderForTier selects the first enabled provider from the given tier.
// Returns (cli, model, providerName) or ("","","") if no provider is available.
func PickProviderForTier(cfg *config.Config, tier string) (cli, model, name string) {
	if cfg == nil {
		return "", "", ""
	}
	for _, n := range tierProviderNames(cfg, tier) {
		p, ok := cfg.Providers[n]
		if !ok || !p.Enabled {
			continue
		}
		return p.CLI, p.Model, n
	}
	return "", "", ""
}

// PickProvider selects the first enabled provider starting from the given tier
// and escalating up the chain. If no provider is available in that chain, it
// falls back to any enabled provider in deterministic order.
func PickProvider(cfg *config.Config, startTier string) (cli, model, tier string) {
	if cfg == nil {
		return "", "", ""
	}
	for _, t := range escChain(startTier) {
		c, m, _ := PickProviderForTier(cfg, t)
		if c != "" {
			return c, m, t
		}
	}
	// Secondary fallback: try any enabled provider from all tiers.
	for _, t := range TierOrder {
		c, m, _ := PickProviderForTier(cfg, t)
		if c != "" {
			return c, m, t
		}
	}
	// Last resort: any enabled provider not listed in tiers.
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := cfg.Providers[name]
		if !p.Enabled || strings.TrimSpace(p.CLI) == "" {
			continue
		}
		resolvedTier := strings.ToLower(strings.TrimSpace(p.Tier))
		if resolvedTier == "" {
			resolvedTier = "balanced"
		}
		return p.CLI, p.Model, resolvedTier
	}
	return "", "", ""
}

// escChain returns the escalation chain starting from the given tier.
func escChain(start string) []string {
	found := false
	var chain []string
	for _, t := range TierOrder {
		if t == start {
			found = true
		}
		if found {
			chain = append(chain, t)
		}
	}
	if !found {
		return TierOrder
	}
	return chain
}

// tierProviderNames returns the ordered provider name list for a tier.
func tierProviderNames(cfg *config.Config, tier string) []string {
	switch tier {
	case "fast":
		return cfg.Tiers.Fast
	case "balanced":
		return cfg.Tiers.Balanced
	case "premium":
		return cfg.Tiers.Premium
	default:
		return nil
	}
}
