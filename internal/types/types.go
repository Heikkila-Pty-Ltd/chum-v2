// Package types defines shared types and utilities used across CHUM packages.
package types

// DecompStep is a single sub-task produced by decomposition.
type DecompStep struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Acceptance  string `json:"acceptance"`
	Estimate    int    `json:"estimate_minutes"`
}

// DecompResult is the output of a decomposition activity.
type DecompResult struct {
	Steps  []DecompStep `json:"steps"`
	Atomic bool         `json:"atomic"` // true when steps is empty (task needs no decomposition)
}

// Task status constants used across the DAG, engine, and planning packages.
const (
	StatusOpen            = "open"
	StatusReady           = "ready"
	StatusRunning         = "running"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
	StatusDecomposed      = "decomposed"
	StatusDoDFailed       = "dod_failed"
	StatusNeedsRefinement = "needs_refinement"
	StatusStale           = "stale"
)

// Truncate returns s truncated to maxLen runes with "..." appended if truncated.
func Truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
