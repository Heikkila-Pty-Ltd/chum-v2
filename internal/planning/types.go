package planning

import (
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
)

// PlanningRequest represents the input to start a planning workflow
type PlanningRequest struct {
	IssueID     string `json:"issue_id"`
	Project     string `json:"project"`
	WorkDir     string `json:"work_dir"`
	LLMAgent    string `json:"llm_agent"`
	LLMModel    string `json:"llm_model"`
}

// PlanningPhase represents the current phase of planning
type PlanningPhase string

const (
	PhaseClarify    PlanningPhase = "clarify"
	PhaseResearch   PlanningPhase = "research"
	PhaseSelect     PlanningPhase = "select"
	PhaseGreenlight PlanningPhase = "greenlight"
	PhaseDecompose  PlanningPhase = "decompose"
	PhaseApprove    PlanningPhase = "approve"
	PhaseHandoff    PlanningPhase = "handoff"
	PhaseCompleted  PlanningPhase = "completed"
)

// PlanningSignal represents signals sent to the workflow during interactive phases
type PlanningSignal struct {
	Phase    PlanningPhase `json:"phase"`
	Decision string        `json:"decision"`
	Data     interface{}   `json:"data,omitempty"`
}

// ClarifyResult contains the output of the clarify phase
type ClarifyResult struct {
	ClarifiedRequirements string   `json:"clarified_requirements"`
	Questions             []string `json:"questions,omitempty"`
	EstimatedComplexity   string   `json:"estimated_complexity"`
}

// ResearchResult contains the output of the research phase
type ResearchResult struct {
	CodebaseAnalysis   string   `json:"codebase_analysis"`
	RelevantFiles      []string `json:"relevant_files"`
	Dependencies       []string `json:"dependencies"`
	RiskAssessment     string   `json:"risk_assessment"`
}

// SelectResult contains the output of the select phase
type SelectResult struct {
	SelectedApproach   string   `json:"selected_approach"`
	AlternativeOptions []string `json:"alternative_options"`
	Rationale         string   `json:"rationale"`
	ImplementationPlan string   `json:"implementation_plan"`
}

// GreenlightResult contains the decision from the greenlight phase
type GreenlightResult struct {
	Approved       bool   `json:"approved"`
	Feedback       string `json:"feedback,omitempty"`
	Modifications  string `json:"modifications,omitempty"`
}

// DecomposeResult contains the subtasks created during decomposition
type DecomposeResult struct {
	Subtasks []SubtaskSpec `json:"subtasks"`
	Timeline string        `json:"timeline"`
}

// SubtaskSpec defines a subtask to be created
type SubtaskSpec struct {
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	EstimateMinutes int      `json:"estimate_minutes"`
	Priority        int      `json:"priority"`
	Dependencies    []string `json:"dependencies,omitempty"`
	Labels          []string `json:"labels,omitempty"`
}

// ApprovalResult contains the approval decision for the decomposed plan
type ApprovalResult struct {
	Approved        bool              `json:"approved"`
	RejectedTasks   []string          `json:"rejected_tasks,omitempty"`
	TaskModifications map[string]string `json:"task_modifications,omitempty"`
	Feedback        string            `json:"feedback,omitempty"`
}

// HandoffResult contains the final results of the planning ceremony
type HandoffResult struct {
	CreatedTaskIDs   []string `json:"created_task_ids"`
	PlanningDecision string   `json:"planning_decision"`
	Summary          string   `json:"summary"`
}

// PlanningState tracks the current state of the workflow
type PlanningState struct {
	Phase            PlanningPhase     `json:"phase"`
	OriginalIssue    beads.Issue      `json:"original_issue"`
	ClarifyResult    *ClarifyResult   `json:"clarify_result,omitempty"`
	ResearchResult   *ResearchResult  `json:"research_result,omitempty"`
	SelectResult     *SelectResult    `json:"select_result,omitempty"`
	GreenlightResult *GreenlightResult `json:"greenlight_result,omitempty"`
	DecomposeResult  *DecomposeResult `json:"decompose_result,omitempty"`
	ApprovalResult   *ApprovalResult  `json:"approval_result,omitempty"`
	HandoffResult    *HandoffResult   `json:"handoff_result,omitempty"`
	DecisionRecord   string           `json:"decision_record"`
}