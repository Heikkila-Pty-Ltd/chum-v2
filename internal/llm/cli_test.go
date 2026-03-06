package llm

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestIsRateLimited(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		input  string
		expect bool
	}{
		{name: "usage limit", input: "Usage limit reached for today", expect: true},
		{name: "rate limit uppercase", input: "RATE LIMIT EXCEEDED", expect: true},
		{name: "capacity", input: "The service is at capacity", expect: true},
		{name: "normal output", input: "all good", expect: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsRateLimited(tc.input)
			if got != tc.expect {
				t.Fatalf("IsRateLimited(%q) = %v, want %v", tc.input, got, tc.expect)
			}
		})
	}
}

func TestRunWithPromptSuccess(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sh", "-c", "read line; echo \"$line\"")
	res, err := RunWithPrompt(cmd, "hello-stdin", "claude")
	if err != nil {
		t.Fatalf("RunWithPrompt unexpected error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Output, "hello-stdin") {
		t.Fatalf("expected output to contain prompt text, got %q", res.Output)
	}
}

func TestRunWithPromptRateLimited(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sh", "-c", "cat >/dev/null; echo 'RATE LIMIT exceeded'; exit 2")
	res, err := RunWithPrompt(cmd, "prompt", "claude")
	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
	if res == nil {
		t.Fatal("expected CLIResult, got nil")
	}
	if res.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", res.ExitCode)
	}
}

func TestBuildPlanCommandShape(t *testing.T) {
	t.Parallel()

	cmd := BuildPlanCommand(context.Background(), "claude", "claude-sonnet", "/tmp")
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "--print") {
		t.Fatalf("plan command missing --print: %v", cmd.Args)
	}
	if !strings.Contains(args, "--output-format json") {
		t.Fatalf("plan command missing json output format: %v", cmd.Args)
	}
	if !strings.Contains(args, "--model claude-sonnet") {
		t.Fatalf("plan command missing model flag: %v", cmd.Args)
	}
}

func TestBuildExecCommandShape(t *testing.T) {
	t.Parallel()

	cmd := BuildExecCommand(context.Background(), "claude", "claude-sonnet", "/tmp")
	args := strings.Join(cmd.Args, " ")

	if strings.Contains(args, "--print") {
		t.Fatalf("exec command must not include --print: %v", cmd.Args)
	}
	if !strings.Contains(args, "--dangerously-skip-permissions") {
		t.Fatalf("exec command missing unattended flag: %v", cmd.Args)
	}
	if !strings.Contains(args, "--model claude-sonnet") {
		t.Fatalf("exec command missing model flag: %v", cmd.Args)
	}
}

func TestBuildPlanCommand_GeminiShape(t *testing.T) {
	t.Parallel()

	cmd := BuildPlanCommand(context.Background(), "gemini", "gemini-2.5-flash", "/tmp")
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "--prompt  ") {
		t.Fatalf("gemini plan command missing --prompt headless flag: %v", cmd.Args)
	}
	if !strings.Contains(args, "--approval-mode plan") {
		t.Fatalf("gemini plan command missing plan approval mode: %v", cmd.Args)
	}
	if strings.Contains(args, "--print") {
		t.Fatalf("gemini plan command must not use legacy --print: %v", cmd.Args)
	}
}

func TestBuildPlanCommand_CodexShape(t *testing.T) {
	t.Parallel()

	cmd := BuildPlanCommand(context.Background(), "codex", "o4-mini", "/tmp")
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, " exec ") {
		t.Fatalf("codex plan command missing exec subcommand: %v", cmd.Args)
	}
	if !strings.Contains(args, "--sandbox read-only") {
		t.Fatalf("codex plan command missing read-only sandbox: %v", cmd.Args)
	}
	if strings.Contains(args, "--quiet") {
		t.Fatalf("codex plan command must not use removed --quiet flag: %v", cmd.Args)
	}
}

func TestBuildExecCommand_CodexShape(t *testing.T) {
	t.Parallel()

	cmd := BuildExecCommand(context.Background(), "codex", "o4-mini", "/tmp")
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, " exec ") {
		t.Fatalf("codex exec command missing exec subcommand: %v", cmd.Args)
	}
	if !strings.Contains(args, "--full-auto") {
		t.Fatalf("codex exec command missing --full-auto: %v", cmd.Args)
	}
	if strings.Contains(args, "--quiet") {
		t.Fatalf("codex exec command must not use removed --quiet flag: %v", cmd.Args)
	}
}
