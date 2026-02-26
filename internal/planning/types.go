// Package planning implements the push-based planning ceremony for CHUM v2.
// The ceremony runs autonomous research before contacting the human,
// then drives interactive decisions via Matrix chat signals.
package planning

import "time"

// PlanningRequest is the input to the PlanningWorkflow.
type PlanningRequest struct {
	GoalID    string `json:"goal_id"`    // beads issue ID for the goal
	Project   string `json:"project"`    // project name from config
	WorkDir   string `json:"work_dir"`   // project workspace root
	Agent     string `json:"agent"`      // CLI name (claude, gemini, codex)
	Model     string `json:"model"`      // optional model override
	RoomID    string `json:"room_id"`    // Matrix room for push notifications
	Source    string `json:"source"`     // who triggered (matrix-control, cli)
	SessionID string `json:"session_id"` // workflow-assigned unique ID
}

// PlanningCeremonyConfig holds ceremony-level knobs passed to the workflow.
// Populated from config.Planning at workflow start time.
type PlanningCeremonyConfig struct {
	MaxResearchRounds int           `json:"max_research_rounds"`
	SignalTimeout     time.Duration `json:"signal_timeout"`
	SessionTimeout    time.Duration `json:"session_timeout"`
	MaxCycles         int           `json:"max_cycles"`
}

// ClarifiedGoal is the output of goal clarification.
type ClarifiedGoal struct {
	Intent      string   `json:"intent"`
	Constraints []string `json:"constraints"`
	Why         string   `json:"why"`
	Raw         string   `json:"raw"` // original task description
}

// ResearchedApproach is a ranked approach from the research phase.
type ResearchedApproach struct {
	ID          string  `json:"id"`          // bead ID once stored
	Title       string  `json:"title"`       // short name
	Description string  `json:"description"` // how it works
	Tradeoffs   string  `json:"tradeoffs"`   // pros/cons
	Confidence  float64 `json:"confidence"`  // 0.0-1.0 estimated success probability
	Rank        int     `json:"rank"`        // 1-based rank
	Status      string  `json:"status"`      // exploring, selected, blocked, approved
}

// QuestionAnswer is a single Q&A exchange during the interactive phase.
type QuestionAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Round    int    `json:"round"`
}

// DecompStep is a sub-task from decomposition (reuses engine type contract).
type DecompStep struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Acceptance  string `json:"acceptance"`
	Estimate    int    `json:"estimate_minutes"`
}

// PlanningResult is the final output of the PlanningWorkflow.
type PlanningResult struct {
	GoalID           string               `json:"goal_id"`
	SelectedApproach *ResearchedApproach  `json:"selected_approach"`
	SubtaskIDs       []string             `json:"subtask_ids"`
	Cancelled        bool                 `json:"cancelled"`
	CancelReason     string               `json:"cancel_reason"`
	Approaches       []ResearchedApproach `json:"approaches"`
}

// --- Ceremony phase tracking ---

// Phase represents the current stage of the planning ceremony.
type Phase string

const (
	PhaseGoalClarification Phase = "goal_clarification"
	PhaseResearch          Phase = "research"
	PhaseGoalCheck         Phase = "goal_check"
	PhasePushApproaches    Phase = "push_approaches"
	PhaseInteractive       Phase = "interactive"
	PhaseDecompose         Phase = "decompose"
	PhaseApproveDecomp     Phase = "approve_decomposition"
	PhaseHandoff           Phase = "handoff"
	PhaseCancelled         Phase = "cancelled"
	PhaseCompleted         Phase = "completed"
)

// --- Signal types for Temporal workflow signals ---

// SignalSelect is sent when the user selects an approach.
type SignalSelect struct {
	ApproachID string `json:"approach_id"`
}

// SignalDig is sent when the user wants deeper research on an approach.
type SignalDig struct {
	ApproachID string `json:"approach_id"`
	Feedback   string `json:"feedback"` // optional direction
}

// SignalQuestion is sent when the user asks a question about an approach.
type SignalQuestion struct {
	Question string `json:"question"`
}

// SignalGreenlight is sent when the user approves moving to decomposition.
type SignalGreenlight struct {
	Decision string `json:"decision"` // "GO" or "REALIGN"
}

// SignalApproveDecomp is sent when the user approves the decomposition.
type SignalApproveDecomp struct {
	Approved bool   `json:"approved"`
	Feedback string `json:"feedback"` // if rejected, why
}

// SignalCancel is sent to cancel the ceremony.
type SignalCancel struct {
	Reason string `json:"reason"`
}

// Signal channel names used by the workflow.
const (
	SignalNameSelect       = "plan-select"
	SignalNameDig          = "plan-dig"
	SignalNameQuestion     = "plan-question"
	SignalNameGreenlight   = "plan-greenlight"
	SignalNameApproveDecomp = "plan-approve-decomp"
	SignalNameCancel       = "plan-cancel"
)

// --- Chat prompt types ---

// PlanningPrompt is the next actionable prompt sent to the user via chat.
type PlanningPrompt struct {
	SessionID      string   `json:"session_id"`
	Phase          Phase    `json:"phase"`
	Status         string   `json:"status"`
	ExpectedSignal string   `json:"expected_signal"`
	Prompt         string   `json:"prompt"`
	Options        []string `json:"options"`
	Recommendation string   `json:"recommendation"`
}

// PlanningStatus captures coarse workflow state for control loops.
type PlanningStatus struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Phase     Phase  `json:"phase"`
	Status    string `json:"status"`
	Note      string `json:"note"`
}
