// Package llm provides shared LLM CLI invocation utilities.
// Both engine and planning packages use these to run LLM CLIs.
package llm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrRateLimited is returned when the LLM CLI output indicates a rate/usage limit.
var ErrRateLimited = errors.New("rate limited")

// rateLimitPatterns are substrings that indicate rate/usage limits in CLI output.
var rateLimitPatterns = []string{
	"usage limit",
	"rate limit",
	"quota exceeded",
	"try again at",
	"rate_limit_exceeded",
	"too many requests",
	"capacity",
	"overloaded",
}

// IsRateLimited checks whether CLI output indicates a rate/usage limit.
func IsRateLimited(output string) bool {
	lower := strings.ToLower(output)
	for _, pat := range rateLimitPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// CLIResult holds the output of a CLI invocation.
type CLIResult struct {
	ExitCode int
	Output   string
}

// RunCLI executes an LLM CLI in PLAN mode (--print, stdout capture only).
// The CLI does NOT modify files — it just returns text output.
func RunCLI(agent, model, workDir, prompt string) (*CLIResult, error) {
	cmd := BuildPlanCommand(agent, model, workDir)
	return RunWithPrompt(cmd, prompt, agent)
}

// RunCLIExec executes an LLM CLI in EXECUTE mode (file-modifying).
// The CLI WILL modify files in workDir. No --print flag.
func RunCLIExec(agent, model, workDir, prompt string) (*CLIResult, error) {
	cmd := BuildExecCommand(agent, model, workDir)
	return RunWithPrompt(cmd, prompt, agent)
}

// RunWithPrompt executes a pre-built command with a prompt piped via stdin.
func RunWithPrompt(cmd *exec.Cmd, prompt, agent string) (*CLIResult, error) {
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")

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

	if IsRateLimited(result.Output) {
		return result, fmt.Errorf("%w: %s", ErrRateLimited, agent)
	}

	return result, nil
}

// FilterEnv returns a copy of env with the named variable removed.
func filterEnv(env []string, name string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// BuildPlanCommand creates a CLI command for PLANNING (text output only).
func BuildPlanCommand(agent, model, workDir string) *exec.Cmd {
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
		args := []string{"--quiet"}
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

// BuildExecCommand creates a CLI command for EXECUTION (file-modifying).
func BuildExecCommand(agent, model, workDir string) *exec.Cmd {
	agent = strings.ToLower(agent)
	var cmd *exec.Cmd
	switch {
	case strings.HasPrefix(agent, "claude"):
		args := []string{"--dangerously-skip-permissions"}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.Command("claude", args...)
	case strings.HasPrefix(agent, "gemini"):
		args := []string{"--sandbox=false"}
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
