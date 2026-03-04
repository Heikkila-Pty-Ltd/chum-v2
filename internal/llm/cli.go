// Package llm provides shared LLM CLI invocation utilities.
// Both engine and planning packages use these to run LLM CLIs.
package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
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
	ExitCode     int
	Output       string
	InputTokens  int     // extracted from CLI output (best-effort)
	OutputTokens int     // extracted from CLI output (best-effort)
	CostUSD      float64 // extracted from CLI output (best-effort)
	LatencyMs    int64   // wall-clock execution time
}

// RunCLI executes an LLM CLI in PLAN mode (--print, stdout capture only).
// The CLI does NOT modify files — it just returns text output.
func RunCLI(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error) {
	cmd := BuildPlanCommand(ctx, agent, model, workDir)
	return RunWithPrompt(cmd, prompt, agent)
}

// RunCLIExec executes an LLM CLI in EXECUTE mode (file-modifying).
// The CLI WILL modify files in workDir. No --print flag.
func RunCLIExec(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error) {
	cmd := BuildExecCommand(ctx, agent, model, workDir)
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

	start := time.Now()
	out, err := cmd.CombinedOutput()
	latency := time.Since(start)

	result := &CLIResult{
		Output:    string(out),
		LatencyMs: latency.Milliseconds(),
	}

	// Extract token usage from output (best-effort, never errors).
	usage := ParseCLIUsage(result.Output, agent)
	result.InputTokens = usage.InputTokens
	result.OutputTokens = usage.OutputTokens
	result.CostUSD = usage.CostUSD

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
	}

	if result.ExitCode != 0 {
		if IsRateLimited(result.Output) {
			return result, fmt.Errorf("%w: %s", ErrRateLimited, agent)
		}
		return result, fmt.Errorf("CLI %s exited with code %d: %s", agent, result.ExitCode, types.Truncate(strings.TrimSpace(result.Output), 200))
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

// providerConfig defines how to invoke a specific LLM CLI.
type providerConfig struct {
	binary    string
	planFlags []string
	execFlags []string
}

// providers maps agent name prefixes to their CLI configurations.
var providers = map[string]providerConfig{
	"claude": {
		binary:    "claude",
		planFlags: []string{"--print", "--output-format", "json"},
		execFlags: []string{"--dangerously-skip-permissions"},
	},
	"gemini": {
		binary:    "gemini",
		planFlags: []string{"--print"},
		execFlags: []string{"--sandbox=false"},
	},
	"codex": {
		binary:    "codex",
		planFlags: []string{"--quiet"},
		execFlags: []string{"--quiet", "--full-auto"},
	},
}

// providerPrefixes returns provider keys sorted by length descending,
// so longer prefixes match before shorter ones (e.g. "codex" before "code").
func providerPrefixes() []string {
	keys := make([]string, 0, len(providers))
	for k := range providers {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j])
	})
	return keys
}

// normalizeCLIName extracts the canonical CLI binary name from an agent string.
func normalizeCLIName(agent string) string {
	agent = strings.ToLower(agent)
	for _, prefix := range providerPrefixes() {
		if strings.HasPrefix(agent, prefix) {
			return providers[prefix].binary
		}
	}
	return agent
}

func buildCommand(ctx context.Context, agent, model, workDir string, modeFlags func(providerConfig) []string) *exec.Cmd {
	lower := strings.ToLower(agent)
	for _, prefix := range providerPrefixes() {
		if strings.HasPrefix(lower, prefix) {
			cfg := providers[prefix]
			args := append([]string{}, modeFlags(cfg)...)
			if model != "" {
				args = append(args, "--model", model)
			}
			cmd := exec.CommandContext(ctx, cfg.binary, args...)
			cmd.Dir = workDir
			return cmd
		}
	}
	cmd := exec.CommandContext(ctx, agent)
	cmd.Dir = workDir
	return cmd
}

// BuildPlanCommand creates a CLI command for PLANNING (text output only).
func BuildPlanCommand(ctx context.Context, agent, model, workDir string) *exec.Cmd {
	return buildCommand(ctx, agent, model, workDir, func(p providerConfig) []string { return p.planFlags })
}

// BuildExecCommand creates a CLI command for EXECUTION (file-modifying).
func BuildExecCommand(ctx context.Context, agent, model, workDir string) *exec.Cmd {
	return buildCommand(ctx, agent, model, workDir, func(p providerConfig) []string { return p.execFlags })
}
