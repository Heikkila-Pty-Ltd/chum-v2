// Package engine implements the Temporal workflows and activities for CHUM v2.
package engine

// TaskRequest is the input to the AgentWorkflow.
// Tasks arrive fully planned and scoped from beads — description, acceptance
// criteria, and design notes are all in the Prompt field.
type TaskRequest struct {
	TaskID  string `json:"task_id"`
	Project string `json:"project"`
	Prompt  string `json:"prompt"`   // full task context from beads
	WorkDir string `json:"work_dir"` // project workspace root
	Agent   string `json:"agent"`    // CLI name (claude, gemini, codex)
	Model   string `json:"model"`    // optional model override
}

// ExecResult is the output of the execute activity.
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// PRInfo captures pull request metadata needed by the workflow.
type PRInfo struct {
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
	URL     string `json:"url"`
}

// ReviewDraft is the parsed output of the reviewer model in print mode.
type ReviewDraft struct {
	Signal        string `json:"signal"`
	Body          string `json:"body"`
	ReviewerAgent string `json:"reviewer_agent"`
	ReviewerModel string `json:"reviewer_model"`
}

// ReviewOutcome is the normalized review state observed on GitHub.
type ReviewOutcome string

const (
	ReviewApproved         ReviewOutcome = "approved"
	ReviewChangesRequested ReviewOutcome = "changes_requested"
	ReviewNoActivity       ReviewOutcome = "no_review_activity"
	ReviewerFailed         ReviewOutcome = "reviewer_failed"
)

// ReviewResult is a structured state result from GitHub review queries.
type ReviewResult struct {
	Outcome   ReviewOutcome `json:"outcome"`
	Reason    string        `json:"reason"`
	ReviewURL string        `json:"review_url"`
	Comments  string        `json:"comments"`
	ReviewID  int64         `json:"review_id"`
}

// MergeResult captures merge attempt status.
type MergeResult struct {
	Merged    bool   `json:"merged"`
	SubReason string `json:"sub_reason"`
	Reason    string `json:"reason"`
}

// CloseReason is the final task close status.
type CloseReason string

const (
	CloseCompleted   CloseReason = "completed"
	CloseDoDFailed   CloseReason = "dod_failed"
	CloseNeedsReview CloseReason = "needs_review"
)

// CloseDetail is persisted for auditability in task error_log.
type CloseDetail struct {
	Reason    CloseReason `json:"reason"`
	SubReason string      `json:"sub_reason"`
	ReviewURL string      `json:"review_url"`
	PRNumber  int         `json:"pr_number"`
}
