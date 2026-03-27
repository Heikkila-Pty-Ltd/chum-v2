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

// Status represents the state of a task in the system.
type Status string

// Task status constants used across the DAG, engine, and planning packages.
const (
	StatusOpen            Status = "open"
	StatusReady           Status = "ready"
	StatusApproved        Status = "approved"
	StatusRunning         Status = "running"
	StatusCompleted       Status = "completed"
	StatusDone            Status = "done" // legacy synonym for completed
	StatusFailed          Status = "failed"
	StatusNeedsReview     Status = "needs_review"
	StatusRejected        Status = "rejected"
	StatusDecomposed      Status = "decomposed"
	StatusDoDFailed       Status = "dod_failed"
	StatusNeedsRefinement Status = "needs_refinement"
	StatusStale           Status = "stale"
)

// Truncate returns s truncated to maxLen runes with "..." appended if truncated.
func Truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
