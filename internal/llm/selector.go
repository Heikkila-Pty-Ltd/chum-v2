package llm

import (
	"strings"
)

// ProviderConfig holds what we need from a provider entry.
type ProviderConfig struct {
	CLI     string
	Model   string
	Tier    string
	Enabled bool
}

// TierConfig maps tier names to ordered provider lists.
type TierConfig struct {
	Fast     []string
	Balanced []string
	Premium  []string
}

// tierOrder is the escalation chain.
var tierOrder = []string{"fast", "balanced", "premium"}

// NewConfigSelector builds a ProviderSelector from provider and tier configuration.
// It returns the next provider in the escalation chain above the current tier.
func NewConfigSelector(providers map[string]ProviderConfig, tiers TierConfig) ProviderSelector {
	// Track which tiers we've already tried to avoid loops.
	tried := make(map[string]bool)

	return func(currentTier string) *ProviderOption {
		tried[currentTier] = true

		// Find the next tier in the chain.
		chain := escChain(currentTier)
		for _, tier := range chain {
			if tried[tier] {
				continue
			}
			tried[tier] = true

			// Find first enabled provider in this tier.
			names := tierNames(tiers, tier)
			for _, name := range names {
				p, ok := providers[name]
				if !ok || !p.Enabled {
					continue
				}
				return &ProviderOption{
					Agent: p.CLI,
					Model: p.Model,
					Tier:  tier,
				}
			}
		}
		return nil
	}
}

func escChain(start string) []string {
	found := false
	var chain []string
	for _, t := range tierOrder {
		if strings.EqualFold(t, start) {
			found = true
		}
		if found {
			chain = append(chain, t)
		}
	}
	if !found {
		return tierOrder
	}
	return chain
}

func tierNames(tc TierConfig, tier string) []string {
	switch tier {
	case "fast":
		return tc.Fast
	case "balanced":
		return tc.Balanced
	case "premium":
		return tc.Premium
	default:
		return nil
	}
}
