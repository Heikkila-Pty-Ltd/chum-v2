// Package engine implements the Temporal workflows and activities for CHUM v2.
package engine

// TaskRequest is the input to the AgentWorkflow.
type TaskRequest struct {
	TaskID  string `json:"task_id"`
	Project string `json:"project"`
	Prompt  string `json:"prompt"`
	WorkDir string `json:"work_dir"` // project workspace root
	Agent   string `json:"agent"`    // CLI name (claude, gemini, codex)
	Model   string `json:"model"`    // optional model override
}

// Plan is the output of the planning activity.
type Plan struct {
	Summary       string   `json:"summary"`
	Steps         []string `json:"steps"`
	FilesToModify []string `json:"files_to_modify"`
	Acceptance    []string `json:"acceptance_criteria"`
}

// Validate checks minimum plan quality.
func (p *Plan) Validate() []string {
	var issues []string
	if p.Summary == "" {
		issues = append(issues, "missing summary")
	}
	if len(p.Steps) == 0 {
		issues = append(issues, "no steps")
	}
	return issues
}

// ExecResult is the output of the execute activity.
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}
