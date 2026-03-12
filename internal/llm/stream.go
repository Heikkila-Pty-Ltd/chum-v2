package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// StreamChunk represents a piece of streaming LLM output.
type StreamChunk struct {
	Text  string // incremental text token
	Done  bool   // true on final chunk
	Error error  // non-nil if CLI exited with an error
}

// streamEvent represents a JSON line from --output-format stream-json.
type streamEvent struct {
	Type  string `json:"type"`
	Event *struct {
		Type  string `json:"type"`
		Delta *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
}

// RunCLIStream executes an LLM CLI in plan mode, streaming token-by-token
// via --output-format stream-json --include-partial-messages.
func RunCLIStream(ctx context.Context, agent, model, workDir, prompt string) (<-chan StreamChunk, error) {
	cmd := buildStreamCommand(ctx, agent, model, workDir)
	cmd.Env = filterEnv(filterEnv(os.Environ(), "CLAUDECODE"), "CLAUDE_CODE_ENTRYPOINT")

	// Write prompt to temp file and pipe via stdin.
	tmpFile, err := os.CreateTemp("", "chum-prompt-*.txt")
	if err != nil {
		return nil, fmt.Errorf("create prompt file: %w", err)
	}
	if _, err := tmpFile.WriteString(prompt); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("write prompt: %w", err)
	}
	tmpFile.Close()

	stdinFile, err := os.Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("reopen prompt file: %w", err)
	}
	cmd.Stdin = stdinFile

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdinFile.Close()
		os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Capture stderr for error reporting.
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		stdinFile.Close()
		os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("start CLI: %w", err)
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer os.Remove(tmpFile.Name())
		defer stdinFile.Close()

		scanner := bufio.NewScanner(stdout)
		// Increase buffer for long JSON lines.
		scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

		for scanner.Scan() {
			line := scanner.Bytes()

			var ev streamEvent
			if json.Unmarshal(line, &ev) != nil {
				continue
			}

			// Extract text deltas from content_block_delta events.
			if ev.Type == "stream_event" && ev.Event != nil &&
				ev.Event.Type == "content_block_delta" &&
				ev.Event.Delta != nil && ev.Event.Delta.Type == "text_delta" {
				select {
				case ch <- StreamChunk{Text: ev.Event.Delta.Text}:
				case <-ctx.Done():
					_ = cmd.Process.Kill()
					return
				}
			}
		}

		var streamErr error
		if scanErr := scanner.Err(); scanErr != nil {
			streamErr = fmt.Errorf("stream read: %w", scanErr)
		}
		if waitErr := cmd.Wait(); waitErr != nil && streamErr == nil {
			stderr := strings.TrimSpace(stderrBuf.String())
			if stderr != "" {
				streamErr = fmt.Errorf("CLI exited: %w: %s", waitErr, stderr)
			} else {
				streamErr = fmt.Errorf("CLI exited: %w", waitErr)
			}
		}
		ch <- StreamChunk{Done: true, Error: streamErr}
	}()

	return ch, nil
}

// buildStreamCommand creates a CLI command for token-level streaming.
func buildStreamCommand(ctx context.Context, agent, model, workDir string) *exec.Cmd {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	binary := normalizeCLIName(agent)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workDir
	return cmd
}
