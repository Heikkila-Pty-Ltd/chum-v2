package plansession

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Event types for bridge events.
const (
	EventToken            = "token"
	EventTurnComplete     = "turn_complete"
	EventToolUse          = "tool_use"
	EventSessionError     = "session_error"
	EventSessionDestroyed = "session_destroyed"
)

// BridgeEvent represents a single event from the Claude session.
type BridgeEvent struct {
	Type string
	Data map[string]string
}

// Bridge manages I/O between a tmux session and the SSE handler.
type Bridge struct {
	session *Session
	events  chan BridgeEvent
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// Turn detection.
	quiescenceTimeout time.Duration
}

// NewBridge creates and starts a bridge for the given session.
func NewBridge(sess *Session) (*Bridge, error) {
	ctx, cancel := context.WithCancel(context.Background())

	b := &Bridge{
		session:           sess,
		events:            make(chan BridgeEvent, 256),
		cancel:            cancel,
		quiescenceTimeout: 3 * time.Second,
	}

	sess.bridge = b

	// Start stdout reader goroutine.
	b.wg.Add(1)
	go b.readStdout(ctx)

	return b, nil
}

// Events returns the channel of bridge events. Read-only for consumers.
func (b *Bridge) Events() <-chan BridgeEvent {
	return b.events
}

// SendMessage writes a message to the session's stdin pipe.
// Returns ErrSessionBusy if the session is currently processing.
func (b *Bridge) SendMessage(msg string) error {
	if b.session.State != StateReady {
		return fmt.Errorf("session not ready (state: %s)", b.session.State)
	}

	if !b.session.BusyMu.TryLock() {
		return ErrSessionBusy
	}
	// The mutex will be released by the stdout reader when it detects turn completion.

	// Open the stdin pipe (non-blocking write side).
	f, err := os.OpenFile(b.session.StdinPath, os.O_WRONLY, 0)
	if err != nil {
		b.session.BusyMu.Unlock()
		return fmt.Errorf("open stdin pipe: %w", err)
	}
	defer f.Close()

	// Write the message as a stream-json input event.
	input := fmt.Sprintf(`{"type":"user_input","content":"%s"}`, escapeJSON(msg))
	if _, err := fmt.Fprintln(f, input); err != nil {
		b.session.BusyMu.Unlock()
		return fmt.Errorf("write stdin: %w", err)
	}

	return nil
}

// Stop shuts down the bridge and closes the events channel.
func (b *Bridge) Stop() {
	b.cancel()
	b.wg.Wait()
	close(b.events)
}

// readStdout reads from the stdout pipe and dispatches events.
func (b *Bridge) readStdout(ctx context.Context) {
	defer b.wg.Done()

	// Open stdout pipe — this blocks until the write side (claude) opens it.
	f, err := os.Open(b.session.StdoutPath)
	if err != nil {
		b.sendEvent(ctx, BridgeEvent{
			Type: EventSessionError,
			Data: map[string]string{"message": fmt.Sprintf("open stdout pipe: %v", err), "recoverable": "false"},
		})
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	var lastOutput time.Time
	busyHeld := false

	for {
		select {
		case <-ctx.Done():
			if busyHeld {
				b.session.BusyMu.Unlock()
			}
			return
		default:
		}

		if !scanner.Scan() {
			break
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		// Strip ANSI escape codes.
		line = ansiRe.ReplaceAllString(line, "")
		if line == "" {
			continue
		}

		lastOutput = time.Now()
		busyHeld = true

		// Send token event.
		b.sendEvent(ctx, BridgeEvent{
			Type: EventToken,
			Data: map[string]string{"text": line + "\n"},
		})
	}

	_ = lastOutput // used by turn detection

	// Pipe EOF — session has ended.
	if busyHeld {
		b.session.BusyMu.Unlock()
	}

	if err := scanner.Err(); err != nil {
		b.sendEvent(ctx, BridgeEvent{
			Type: EventSessionError,
			Data: map[string]string{"message": fmt.Sprintf("read stdout: %v", err), "recoverable": "false"},
		})
	}

	b.sendEvent(ctx, BridgeEvent{
		Type: EventSessionDestroyed,
		Data: map[string]string{"reason": "pipe_eof"},
	})
}

// sendEvent sends an event to the channel with context-aware blocking.
// This prevents goroutine leaks when SSE clients disconnect.
func (b *Bridge) sendEvent(ctx context.Context, event BridgeEvent) {
	select {
	case b.events <- event:
	case <-ctx.Done():
	}
}

// escapeJSON escapes a string for embedding in a JSON string value.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}
