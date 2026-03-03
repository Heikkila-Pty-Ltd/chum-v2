// Package perf tracks agent+model performance and selects the best provider
// for a given tier based on historical outcomes.
//
// It reads from execution_traces (already populated by the engine) and uses
// UCT selection (internal/uct) to balance exploitation vs exploration.
// No new tables — just queries on existing data.
package perf

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/uct"
)

// Provider is an agent+model combination that can execute tasks.
type Provider struct {
	Agent string // CLI name: "claude", "codex", "gemini"
	Model string // Model name: "sonnet-4", "gpt-4.1", etc.
	Tier  string // fast, balanced, premium
}

// Key returns a stable identifier for this provider.
func (p Provider) Key() string {
	if p.Model != "" {
		return p.Agent + "/" + p.Model
	}
	return p.Agent
}

// Stats summarizes a provider's historical performance.
type Stats struct {
	Provider    Provider
	TotalRuns   int
	Successes   int
	SuccessRate float64
	AvgDuration float64 // seconds
}

// Tracker queries execution traces to build performance stats and select providers.
type Tracker struct {
	db          *sql.DB
	exploration float64
}

// New creates a Tracker. The exploration parameter controls UCT exploration
// vs exploitation (1.414 is standard, higher = more exploration).
func New(db *sql.DB, exploration float64) *Tracker {
	if exploration <= 0 {
		exploration = 1.414
	}
	return &Tracker{db: db, exploration: exploration}
}

// Record stores the outcome of a task execution. Called by engine after
// each AgentWorkflow completes.
func (t *Tracker) Record(ctx context.Context, agent, model, tier string, success bool, durationS float64) error {
	key := agent
	if model != "" {
		key = agent + "/" + model
	}
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO perf_runs (provider_key, agent, model, tier, success, duration_s)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key, agent, model, tier, successInt, durationS)
	return err
}

// StatsForTier returns performance stats for all providers that have been
// used in the given tier, ordered by success rate descending.
func (t *Tracker) StatsForTier(ctx context.Context, tier string) ([]Stats, error) {
	query := `
		SELECT agent, model, tier,
			COUNT(*) as total_runs,
			SUM(success) as successes,
			CAST(SUM(success) AS REAL) / COUNT(*) as success_rate,
			AVG(duration_s) as avg_duration
		FROM perf_runs
		WHERE tier = ?
		GROUP BY provider_key
		ORDER BY success_rate DESC, total_runs DESC`

	rows, err := t.db.QueryContext(ctx, query, tier)
	if err != nil {
		return nil, fmt.Errorf("perf: stats for tier %q: %w", tier, err)
	}
	defer rows.Close()

	var stats []Stats
	for rows.Next() {
		var s Stats
		if err := rows.Scan(&s.Provider.Agent, &s.Provider.Model, &s.Provider.Tier,
			&s.TotalRuns, &s.Successes, &s.SuccessRate, &s.AvgDuration); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// Pick selects the best provider for a tier using UCT. Returns nil if no
// providers have been recorded for this tier.
func (t *Tracker) Pick(ctx context.Context, tier string) (*Provider, error) {
	stats, err := t.StatsForTier(ctx, tier)
	if err != nil {
		return nil, err
	}
	if len(stats) == 0 {
		return nil, nil // no data yet, caller should fall back to config
	}

	arms := make([]uct.Arm, len(stats))
	for i, s := range stats {
		arms[i] = uct.Arm{
			Key:         s.Provider.Key(),
			Visits:      s.TotalRuns,
			TotalReward: float64(s.Successes),
		}
	}

	sel, ok := uct.Select(arms, t.exploration)
	if !ok {
		return nil, nil
	}

	return &stats[sel.Index].Provider, nil
}
