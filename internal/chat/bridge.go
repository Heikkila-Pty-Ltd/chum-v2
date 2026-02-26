package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
)

func atomicAddTxn() uint64 {
	return atomic.AddUint64(&matrixTxnCounter, 1)
}

// Bridge polls a Matrix room for /plan commands and routes them to Temporal
// workflow signals. It also sends push notifications from workflows to chat.
type Bridge struct {
	Client         client.Client
	MatrixCfg      MatrixConfig
	PollInterval   time.Duration
	Logger         *slog.Logger
	TaskQueue      string
	DefaultAgent   string                         // default LLM agent (e.g. "claude")
	CeremonyCfg    planning.PlanningCeremonyConfig // ceremony-level knobs
	AllowedSenders  map[string]bool                // Matrix user IDs allowed to issue commands (nil = allow all)
	BotUserID       string                         // Matrix user ID of the bot itself — messages from this sender are ignored
	ProjectWorkDirs map[string]string              // project name → workspace path (used to resolve WorkDir securely)

	mu           sync.Mutex
	activeByRoom map[string]string // roomID → active planning session workflow ID
	sinceToken   string
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	if b.PollInterval <= 0 {
		b.PollInterval = 10 * time.Second
	}

	// Resolve the bot's own Matrix user ID so we can filter self-echo.
	if b.BotUserID == "" {
		if uid, err := resolveMatrixUserID(ctx, b.MatrixCfg); err != nil {
			b.Logger.Warn("Failed to resolve bot user ID — self-echo filtering disabled", "error", err)
		} else {
			b.BotUserID = uid
			b.Logger.Info("Bot user ID resolved", "user_id", uid)
		}
	}

	b.Logger.Info("Chat bridge started", "room", b.MatrixCfg.RoomID, "interval", b.PollInterval)

	// Seed the since token to skip historical messages.
	// Loop until we reach the end to avoid replaying old /plan commands.
	// Cap at 500 pages (~10k messages) to prevent spinning on huge rooms.
	token := ""
	for page := 0; page < 500; page++ {
		_, nextToken, err := ReadRoomMessages(ctx, b.MatrixCfg, token)
		if err != nil {
			b.Logger.Warn("Failed to seed message cursor, starting from now", "error", err)
			break
		}
		if nextToken == "" || nextToken == token {
			break
		}
		token = nextToken
	}
	b.sinceToken = token

	ticker := time.NewTicker(b.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			b.poll(ctx)
		}
	}
}

func (b *Bridge) poll(ctx context.Context) {
	msgs, token, err := ReadRoomMessages(ctx, b.MatrixCfg, b.sinceToken)
	if err != nil {
		b.Logger.Warn("Chat poll failed", "error", err)
		return
	}
	if token != "" {
		b.sinceToken = token
	}

	for _, msg := range msgs {
		// Skip messages from the bot itself to prevent self-echo loops.
		if b.BotUserID != "" && msg.Sender == b.BotUserID {
			continue
		}
		cmd, matched, parseErr := ParseCommand(msg.Body)
		if !matched {
			continue
		}
		if b.AllowedSenders != nil && !b.AllowedSenders[msg.Sender] {
			b.Logger.Warn("Rejected planning command from unauthorized sender", "sender", msg.Sender)
			continue
		}
		if parseErr != nil {
			b.Logger.Warn("Malformed planning command", "sender", msg.Sender, "error", parseErr)
			_ = b.send(ctx, fmt.Sprintf("Invalid command: %s. Send `/plan help` for usage.", parseErr))
			continue
		}
		if err := b.handleCommand(ctx, msg, cmd); err != nil {
			b.Logger.Error("Failed to handle planning command", "kind", cmd.Kind, "error", err)
		}
	}
}

func (b *Bridge) handleCommand(ctx context.Context, msg InboundMessage, cmd Command) error {
	switch cmd.Kind {
	case CommandHelp:
		return b.send(ctx, CommandUsage())

	case CommandStart:
		return b.startPlanning(ctx, msg, cmd)

	case CommandSelect:
		return b.signalWorkflow(ctx, msg.Room, cmd.SessionID, "plan-select", cmd.Value)

	case CommandDig:
		payload := cmd.Value
		if cmd.Reason != "" {
			payload += "|" + cmd.Reason
		}
		return b.signalWorkflow(ctx, msg.Room, cmd.SessionID, "plan-dig", payload)

	case CommandAnswer:
		return b.signalWorkflow(ctx, msg.Room, cmd.SessionID, "plan-question", cmd.Value)

	case CommandGo:
		return b.signalWorkflow(ctx, msg.Room, cmd.SessionID, "plan-greenlight", "GO")

	case CommandApprove:
		return b.signalWorkflow(ctx, msg.Room, cmd.SessionID, "plan-approve-decomp", "APPROVED")

	case CommandRealign:
		return b.signalWorkflow(ctx, msg.Room, cmd.SessionID, "plan-greenlight", "REALIGN")

	case CommandStop:
		return b.signalWorkflow(ctx, msg.Room, cmd.SessionID, "plan-cancel", cmd.Reason)

	case CommandPrompt, CommandStatus:
		sessionID := b.resolveSession(msg.Room, cmd.SessionID)
		if sessionID == "" {
			return b.send(ctx, "No active planning session. Start one with /plan start <project>")
		}
		return b.send(ctx, fmt.Sprintf("Active session: %s", sessionID))

	default:
		return b.send(ctx, fmt.Sprintf("Unknown command kind: %d", cmd.Kind))
	}
}

func (b *Bridge) startPlanning(ctx context.Context, msg InboundMessage, cmd Command) error {
	// Resolve WorkDir from project config — never trust user-supplied paths.
	workDir := b.ProjectWorkDirs[cmd.Project]
	if workDir == "" {
		return b.send(ctx, fmt.Sprintf("Unknown project %q. Check your config.", cmd.Project))
	}

	// Guard against duplicate sessions for the same room.
	if existing := b.resolveSession(msg.Room, ""); existing != "" {
		return b.send(ctx, fmt.Sprintf("Planning session already active: `%s`. Send `/plan stop` first, or use `/plan select`/`/plan go` to continue.", existing))
	}

	sessionID := fmt.Sprintf("planning-%s-%d", cmd.Project, time.Now().UnixNano())

	opts := client.StartWorkflowOptions{
		ID:        sessionID,
		TaskQueue: b.TaskQueue,
	}

	agent := cmd.Agent
	if agent == "" {
		agent = b.DefaultAgent
	}

	req := planning.PlanningRequest{
		GoalID:    cmd.Value,
		Project:   cmd.Project,
		WorkDir:   workDir,
		Agent:     agent,
		RoomID:    msg.Room,
		Source:    "matrix-control",
		SessionID: sessionID,
	}

	run, err := b.Client.ExecuteWorkflow(ctx, opts, planning.PlanningWorkflow, req, b.CeremonyCfg)
	if err != nil {
		return fmt.Errorf("start planning workflow: %w", err)
	}

	b.setActiveSession(msg.Room, sessionID)
	return b.send(ctx, fmt.Sprintf("Started planning session `%s` (run: %s) for project %s",
		sessionID, run.GetRunID(), cmd.Project))
}

func (b *Bridge) signalWorkflow(ctx context.Context, room, sessionID, signalName, value string) error {
	sid := b.resolveSession(room, sessionID)
	if sid == "" {
		return b.send(ctx, "No active planning session. Start one with /plan start <project>")
	}

	if err := b.Client.SignalWorkflow(ctx, sid, "", signalName, value); err != nil {
		errStr := err.Error()
		// Clear the active session if the workflow no longer exists.
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "NotFound") ||
			strings.Contains(errStr, "completed") || strings.Contains(errStr, "terminated") {
			b.clearActiveSession(room, sid)
			_ = b.send(ctx, fmt.Sprintf("Session %s is no longer running. Start a new one with `/plan start`.", sid))
			return nil
		}
		_ = b.send(ctx, fmt.Sprintf("Failed to send signal to session %s: %s", sid, err))
		return fmt.Errorf("signal workflow %s: %w", sid, err)
	}

	// If the user just sent a cancel signal, clear the active session.
	if signalName == "plan-cancel" {
		b.clearActiveSession(room, sid)
	}

	return nil
}

// Send sends a message to the configured Matrix room.
func (b *Bridge) Send(ctx context.Context, message string) error {
	return b.send(ctx, message)
}

func (b *Bridge) send(ctx context.Context, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	return SendMatrixMessage(ctx, b.MatrixCfg, message)
}

func (b *Bridge) resolveSession(room, explicit string) string {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeByRoom == nil {
		return ""
	}
	return b.activeByRoom[strings.TrimSpace(room)]
}

func (b *Bridge) setActiveSession(room, sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeByRoom == nil {
		b.activeByRoom = make(map[string]string)
	}
	b.activeByRoom[strings.TrimSpace(room)] = sessionID
}

func (b *Bridge) clearActiveSession(room, sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeByRoom == nil {
		return
	}
	room = strings.TrimSpace(room)
	// Only clear if the current session matches — prevents racing with a new /plan start.
	if b.activeByRoom[room] == sessionID {
		delete(b.activeByRoom, room)
	}
}

// matrixTxnCounter is an atomic counter to ensure unique transaction IDs
// even when multiple messages are sent within the same nanosecond.
var matrixTxnCounter uint64

// SendMatrixMessage sends a text message to a Matrix room using the Client-Server API.
func SendMatrixMessage(ctx context.Context, cfg MatrixConfig, message string) error {
	txnID := fmt.Sprintf("chum-plan-%d-%d", time.Now().UnixNano(), atomicAddTxn())
	path := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		strings.TrimRight(cfg.Homeserver, "/"),
		url.PathEscape(cfg.RoomID),
		txnID,
	)

	payload := map[string]string{
		"msgtype": "m.text",
		"body":    message,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create matrix message request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 8 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send matrix message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("matrix send HTTP %d", resp.StatusCode)
	}
	return nil
}
