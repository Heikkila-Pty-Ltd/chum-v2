package engine

import (
	"os/exec"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
)

// ErrRateLimited is returned when the LLM CLI output indicates a rate/usage limit.
var ErrRateLimited = llm.ErrRateLimited

// IsRateLimited checks whether CLI output indicates a rate/usage limit.
func IsRateLimited(output string) bool {
	return llm.IsRateLimited(output)
}

// CLIResult holds the output of a CLI invocation.
type CLIResult = llm.CLIResult

// RunCLI executes an LLM CLI in PLAN mode (--print, stdout capture only).
func RunCLI(agent, model, workDir, prompt string) (*CLIResult, error) {
	return llm.RunCLI(agent, model, workDir, prompt)
}

// RunCLIExec executes an LLM CLI in EXECUTE mode (file-modifying).
func RunCLIExec(agent, model, workDir, prompt string) (*CLIResult, error) {
	return llm.RunCLIExec(agent, model, workDir, prompt)
}

// runWithPrompt delegates to the llm package. Kept for test compatibility.
func runWithPrompt(cmd *exec.Cmd, prompt, agent string) (*CLIResult, error) {
	return llm.RunWithPrompt(cmd, prompt, agent)
}

// buildPlanCommand delegates to the llm package. Kept for test compatibility.
func buildPlanCommand(agent, model, workDir string) *exec.Cmd {
	return llm.BuildPlanCommand(agent, model, workDir)
}

// buildExecCommand delegates to the llm package. Kept for test compatibility.
func buildExecCommand(agent, model, workDir string) *exec.Cmd {
	return llm.BuildExecCommand(agent, model, workDir)
}
