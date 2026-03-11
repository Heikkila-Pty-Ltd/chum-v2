package llm

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// StreamChunk represents a piece of streaming LLM output.
type StreamChunk struct {
	Text string // incremental text line
	Done bool   // true on final chunk
}

// RunCLIStream executes an LLM CLI in plan mode, streaming stdout line-by-line
// to the provided channel. The channel is closed when the CLI exits.
// Uses --print flag which streams to stdout. We read line-by-line and relay.
func RunCLIStream(ctx context.Context, agent, model, workDir, prompt string) (<-chan StreamChunk, error) {
	cmd := BuildPlanCommand(ctx, agent, model, workDir)
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")

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

	// Capture stderr separately so it doesn't mix into the stream.
	cmd.Stderr = io.Discard

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
		// Increase buffer for long lines from LLM output.
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

		for scanner.Scan() {
			line := scanner.Text()
			// Skip Claude JSON envelope lines (--output-format json wraps the output).
			if isClaudeEnvelope(line) {
				continue
			}
			select {
			case ch <- StreamChunk{Text: line}:
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return
			}
		}

		_ = cmd.Wait()
		ch <- StreamChunk{Done: true}
	}()

	return ch, nil
}

// isClaudeEnvelope checks if a line is a Claude CLI JSON envelope wrapper.
func isClaudeEnvelope(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, `{"type":"result"`)
}
