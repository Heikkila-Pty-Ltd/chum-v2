// Package engine implements the Temporal workflows and activities for CHUM v2.
package engine

// TaskRequest is the input to the AgentWorkflow.
// Tasks arrive fully planned and scoped from beads — description, acceptance
// criteria, and design notes are all in the Prompt field.
type TaskRequest struct {
	TaskID  string `json:"task_id"`
	Project string `json:"project"`
	Prompt  string `json:"prompt"`  // full task context from beads
	WorkDir string `json:"work_dir"` // project workspace root
	Agent   string `json:"agent"`    // CLI name (claude, gemini, codex)
	Model   string `json:"model"`    // optional model override
}

// ExecResult is the output of the execute activity.
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}
