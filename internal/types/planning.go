package types

import "time"

// PlanningGoal captures the clarified user intent that anchors a plan.
type PlanningGoal struct {
	Intent      string   `json:"intent"`
	Constraints []string `json:"constraints"`
	Why         string   `json:"why"`
	Raw         string   `json:"raw"`
}

// PlanningApproach is a storage-safe copy of a researched approach.
type PlanningApproach struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Tradeoffs   string  `json:"tradeoffs"`
	Confidence  float64 `json:"confidence"`
	Rank        int     `json:"rank"`
	Status      string  `json:"status"`
}

// PlanSpec is the contract between planning and execution.
type PlanSpec struct {
	ProblemStatement   string           `json:"problem_statement"`
	DesiredOutcome     string           `json:"desired_outcome"`
	ExpectedPROutcome  string           `json:"expected_pr_outcome"`
	Summary            string           `json:"summary"`
	Goal               PlanningGoal     `json:"goal"`
	ChosenApproach     PlanningApproach `json:"chosen_approach"`
	Assumptions        []string         `json:"assumptions"`
	NonGoals           []string         `json:"non_goals"`
	Risks              []string         `json:"risks"`
	ValidationStrategy []string         `json:"validation_strategy"`
	Steps              []DecompStep     `json:"steps"`
}

// PlanningPhaseEntry captures a phase transition for operator visibility.
type PlanningPhaseEntry struct {
	Phase     string `json:"phase"`
	Status    string `json:"status"`
	Note      string `json:"note"`
	Timestamp int64  `json:"timestamp"`
}

// PlanningSnapshot stores the current planning state and its phase history.
type PlanningSnapshot struct {
	SessionID        string               `json:"session_id"`
	GoalID           string               `json:"goal_id"`
	Project          string               `json:"project"`
	Source           string               `json:"source"`
	Phase            string               `json:"phase"`
	Status           string               `json:"status"`
	CancelReason     string               `json:"cancel_reason,omitempty"`
	DecisionID       string               `json:"decision_id,omitempty"`
	SelectedApproach *PlanningApproach    `json:"selected_approach,omitempty"`
	Approaches       []PlanningApproach   `json:"approaches"`
	Goal             PlanningGoal         `json:"goal"`
	Steps            []DecompStep         `json:"steps"`
	PlanSpec         *PlanSpec            `json:"plan_spec,omitempty"`
	SubtaskIDs       []string             `json:"subtask_ids"`
	History          []PlanningPhaseEntry `json:"history"`
	WorkflowStatus   string               `json:"workflow_status,omitempty"`
	WorkflowActive   bool                 `json:"workflow_active,omitempty"`
	CreatedAt        time.Time            `json:"created_at,omitempty"`
	UpdatedAt        time.Time            `json:"updated_at,omitempty"`
}
