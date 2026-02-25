package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CLIResult holds the output of a CLI invocation.
type CLIResult struct {
	ExitCode int
	Output   string
}

// RunCLI executes an LLM CLI with the given prompt piped via stdin.
// Returns the combined stdout/stderr output.
func RunCLI(agent, model, workDir, prompt string) (*CLIResult, error) {
	cmd := buildCLICommand(agent, model, workDir)

	// Pipe prompt via stdin (not args) to avoid ARG_MAX and /proc leaks
	tmpFile, err := os.CreateTemp("", "chum-prompt-*.txt")
	if err != nil {
		return nil, fmt.Errorf("create prompt file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(prompt); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write prompt: %w", err)
	}
	tmpFile.Close()

	stdinFile, err := os.Open(tmpFile.Name())
	if err != nil {
		return nil, fmt.Errorf("reopen prompt file: %w", err)
	}
	defer stdinFile.Close()
	cmd.Stdin = stdinFile

	out, err := cmd.CombinedOutput()
	result := &CLIResult{Output: string(out)}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
	}
	return result, nil
}

func buildCLICommand(agent, model, workDir string) *exec.Cmd {
	agent = strings.ToLower(agent)
	var cmd *exec.Cmd
	switch {
	case strings.HasPrefix(agent, "claude"):
		args := []string{"--print", "--output-format", "json"}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.Command("claude", args...)
	case strings.HasPrefix(agent, "gemini"):
		args := []string{"--print"}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.Command("gemini", args...)
	case strings.HasPrefix(agent, "codex"):
		args := []string{"--quiet", "--full-auto"}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.Command("codex", args...)
	default:
		cmd = exec.Command(agent)
	}
	cmd.Dir = workDir
	return cmd
}
