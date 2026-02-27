package llm

import "context"

// Runner abstracts LLM CLI invocation for testability.
type Runner interface {
	// Plan runs the LLM CLI in read-only mode (--print). No file mutations.
	Plan(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error)
	// Exec runs the LLM CLI in execution mode (file-modifying).
	Exec(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error)
}

// CLIRunner implements Runner by shelling out to LLM CLI binaries.
type CLIRunner struct{}

func (CLIRunner) Plan(_ context.Context, agent, model, workDir, prompt string) (*CLIResult, error) {
	return RunCLI(agent, model, workDir, prompt)
}

func (CLIRunner) Exec(_ context.Context, agent, model, workDir, prompt string) (*CLIResult, error) {
	return RunCLIExec(agent, model, workDir, prompt)
}

// NormalizeCLIName extracts the canonical CLI binary name from an agent string.
func NormalizeCLIName(agent string) string {
	return normalizeCLIName(agent)
}
