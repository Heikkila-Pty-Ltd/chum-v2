// Package crab implements the CRAB decomposition pipeline.
// Crabs take high-level markdown plans and decompose them into whales
// (epic-level groupings) and morsels (bite-sized executable units)
// for shark consumption.
//
// Pipeline: PARSE → CLARIFY → BLAST SCAN → DECOMPOSE → SCOPE → SIZE → REVIEW → EMIT
package crab

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// DecompositionRequest starts a crab decomposition workflow.
type DecompositionRequest struct {
	PlanID                  string             `json:"plan_id"`
	Project                 string             `json:"project"`
	WorkDir                 string             `json:"work_dir"`
	PlanMarkdown            string             `json:"plan_markdown"`
	Tier                    string             `json:"tier"`                      // LLM tier: "fast", "balanced", or "premium"
	ParentWhaleID           string             `json:"parent_whale_id,omitempty"` // optional parent whale to nest under
	RequireHumanReview      bool               `json:"require_human_review"`      // if true, block at Phase 6 for human signal
	DisablePlanEscalation   bool               `json:"disable_plan_escalation"`   // if true, return failed instead of escalating
	BlastRadius             *BlastRadiusReport `json:"blast_radius,omitempty"`    // pre-scanned dependency analysis
	SlowStepThreshold       time.Duration      `json:"slow_step_threshold,omitempty"`
}

// DecompositionResult is the final output of the crab workflow.
type DecompositionResult struct {
	Status         string       `json:"status"` // "completed", "rejected", "failed", "escalated"
	PlanID         string       `json:"plan_id"`
	WhalesEmitted  []string     `json:"whales_emitted"`
	MorselsEmitted []string     `json:"morsels_emitted"`
	StepMetrics    []StepMetric `json:"step_metrics,omitempty"`
	TotalTokens    TokenUsage   `json:"total_tokens,omitempty"`
}

// BlastRadiusReport summarizes dependency and coupling data for a project.
type BlastRadiusReport struct {
	Language     string    `json:"language"`
	HotFiles     []HotFile `json:"hot_files,omitempty"`
	CircularDeps []string  `json:"circular_deps,omitempty"`
	TotalPkgs    int       `json:"total_pkgs"`
	TotalFiles   int       `json:"total_files"`
	ScanDurMs    int64     `json:"scan_dur_ms"`
	ScanErrors   []string  `json:"scan_errors,omitempty"`
}

// HotFile is a file imported by many others — high blast radius.
type HotFile struct {
	Path       string `json:"path"`
	ImportedBy int    `json:"imported_by"`
}

// ParsedPlan is the output of deterministic markdown parsing.
type ParsedPlan struct {
	Title              string      `json:"title"`
	Context            string      `json:"context"`
	ScopeItems         []ScopeItem `json:"scope_items"`
	AcceptanceCriteria []string    `json:"acceptance_criteria"`
	OutOfScope         []string    `json:"out_of_scope"`
	RawMarkdown        string      `json:"raw_markdown"`
}

// ScopeItem is a single deliverable from the plan's scope section.
type ScopeItem struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"` // true if [x] instead of [ ]
}

// ClarificationResult holds the results of the 3-tier clarification process.
type ClarificationResult struct {
	Resolved        []ClarificationEntry `json:"resolved"`
	NeedsHumanInput bool                 `json:"needs_human_input"`
	HumanQuestions  []string             `json:"human_questions,omitempty"`
	HumanAnswers    string               `json:"human_answers,omitempty"`
	Tokens          TokenUsage           `json:"tokens,omitempty"`
}

// ClarificationEntry is a single resolved question.
type ClarificationEntry struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Source   string `json:"source"` // "lessons_db", "existing_morsels", "chief_llm", "human"
}

// CandidateWhale is a proposed whale (epic-level grouping) from decomposition.
type CandidateWhale struct {
	Index              int               `json:"index"`
	Title              string            `json:"title"`
	Description        string            `json:"description"`
	AcceptanceCriteria string            `json:"acceptance_criteria"`
	Morsels            []CandidateMorsel `json:"morsels"`
	ParentScopeItem    FlexInt           `json:"parent_scope_item"`
}

// CandidateMorsel is a proposed morsel before sizing.
type CandidateMorsel struct {
	Index              int      `json:"index"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	DesignHints        string   `json:"design_hints"`
	FileHints          []string `json:"file_hints,omitempty"`
	DependsOnIndices   []int    `json:"depends_on_indices,omitempty"`
}

// SizedMorsel is a fully-qualified morsel ready for review and DAG emission.
type SizedMorsel struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Design             string   `json:"design"`
	EstimateMinutes    int      `json:"estimate_minutes"`
	Priority           int      `json:"priority"`
	Labels             []string `json:"labels"`
	FileHints          []string `json:"file_hints,omitempty"`
	DependsOnIndices   []int    `json:"depends_on_indices,omitempty"`
	WhaleIndex         int      `json:"whale_index"`
	RiskLevel          string   `json:"risk_level"` // "low", "medium", "high"
	SizingRationale    string   `json:"sizing_rationale"`
}

// EmitResult is the output of the morsel emission activity.
type EmitResult struct {
	WhaleIDs    []string `json:"whale_ids"`
	MorselIDs   []string `json:"morsel_ids"`
	FailedCount int      `json:"failed_count"`
	Details     []string `json:"details"`
}

// StepMetric records the name, duration, and outcome of a single pipeline step.
type StepMetric struct {
	Name      string  `json:"name"`
	DurationS float64 `json:"duration_s"`
	Status    string  `json:"status"` // "ok", "failed", "skipped"
	Slow      bool    `json:"slow,omitempty"`
}

// TokenUsage tracks LLM token consumption.
type TokenUsage struct {
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CostUSD             float64 `json:"cost_usd"`
}

// Add accumulates another TokenUsage into this one.
func (t *TokenUsage) Add(other TokenUsage) {
	t.InputTokens += other.InputTokens
	t.OutputTokens += other.OutputTokens
	t.CacheReadTokens += other.CacheReadTokens
	t.CacheCreationTokens += other.CacheCreationTokens
	t.CostUSD += other.CostUSD
}

// FlexInt accepts both int and string JSON values, coercing strings to 0.
type FlexInt int

// UnmarshalJSON handles LLM responses that return string instead of int.
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexInt(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(s)); parseErr == nil {
			*f = FlexInt(parsed)
		} else {
			*f = 0
		}
		return nil
	}
	*f = 0
	return nil
}
