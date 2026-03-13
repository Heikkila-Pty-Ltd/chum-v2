// Package plansession manages tmux-backed Claude CLI sessions for interactive plan grooming.
// Each plan gets its own tmux session with stdin/stdout pipes bridged to SSE.
package plansession

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
)

// Sentinel errors for programmatic handling.
var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionBusy     = errors.New("session is busy")
	ErrSpawnFailed     = errors.New("session spawn failed")
	ErrMaxSessions     = errors.New("max concurrent sessions reached")
)

// ansiRe strips ANSI escape codes from terminal output. Compiled once at package level.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// State represents the lifecycle state of a planning session.
type State int

const (
	StateStarting State = iota
	StateReady
	StateDone
)

func (s State) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateReady:
		return "ready"
	case StateDone:
		return "done"
	default:
		return "unknown"
	}
}

// Session represents a single tmux-backed Claude CLI planning session.
type Session struct {
	ID     string // tmux session name: "plan-{planID}"
	PlanID string
	State  State
	Err    error // non-nil if State == StateDone due to failure

	PipeDir    string // private directory: ~/.chum/pipes/{planID}/
	StdinPath  string // path to stdin FIFO
	StdoutPath string // path to stdout FIFO

	BusyMu sync.Mutex // held while Claude is responding

	bridge *Bridge // set after bridge is started
}

// Bridge returns the session's bridge, or nil if not started.
func (s *Session) Bridge() *Bridge {
	return s.bridge
}

// Manager manages the lifecycle of plan sessions.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session // planID → Session

	maxSessions int
	logger      *slog.Logger
	token       string // bearer token for API auth
	apiPort     string // dashboard API port
	model       string // LLM model to use
}

// NewManager creates a session manager.
func NewManager(logger *slog.Logger, apiPort, model string) *Manager {
	token := generateToken()
	return &Manager{
		sessions:    make(map[string]*Session),
		maxSessions: 3,
		logger:      logger,
		token:       token,
		apiPort:     apiPort,
		model:       model,
	}
}

// Token returns the bearer token for API authentication.
func (m *Manager) Token() string {
	return m.token
}

// Get returns the session for a plan, or ErrSessionNotFound.
func (m *Manager) Get(planID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[planID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return s, nil
}

// Spawn creates a new tmux session for a plan.
func (m *Manager) Spawn(planID, workDir string) (*Session, error) {
	m.mu.Lock()

	// Check if session already exists and is healthy.
	if existing, ok := m.sessions[planID]; ok && existing.State != StateDone {
		m.mu.Unlock()
		return existing, nil
	}

	// Check concurrency limit.
	active := 0
	for _, s := range m.sessions {
		if s.State != StateDone {
			active++
		}
	}
	if active >= m.maxSessions {
		m.mu.Unlock()
		return nil, ErrMaxSessions
	}

	sess := &Session{
		ID:     fmt.Sprintf("plan-%s", planID),
		PlanID: planID,
		State:  StateStarting,
	}
	m.sessions[planID] = sess
	m.mu.Unlock()

	// Create private pipe directory.
	home, err := os.UserHomeDir()
	if err != nil {
		m.markDone(sess, fmt.Errorf("%w: user home: %v", ErrSpawnFailed, err))
		return nil, sess.Err
	}
	sess.PipeDir = filepath.Join(home, ".chum", "pipes", planID)
	if err := os.MkdirAll(sess.PipeDir, 0700); err != nil {
		m.markDone(sess, fmt.Errorf("%w: mkdir pipes: %v", ErrSpawnFailed, err))
		return nil, sess.Err
	}

	sess.StdoutPath = filepath.Join(sess.PipeDir, "stdout.pipe")
	sess.StdinPath = filepath.Join(sess.PipeDir, "stdin.pipe")

	// Create FIFOs.
	for _, p := range []string{sess.StdoutPath, sess.StdinPath} {
		os.Remove(p) // clean up stale pipes
		if err := mkfifo(p); err != nil {
			m.markDone(sess, fmt.Errorf("%w: mkfifo %s: %v", ErrSpawnFailed, p, err))
			return nil, sess.Err
		}
	}

	// Generate tools.sh and session CLAUDE.md.
	toolsPath := filepath.Join(sess.PipeDir, "tools.sh")
	if err := writeToolsScript(toolsPath, m.apiPort, m.token); err != nil {
		m.markDone(sess, fmt.Errorf("%w: tools.sh: %v", ErrSpawnFailed, err))
		return nil, sess.Err
	}

	claudeMDPath := filepath.Join(sess.PipeDir, "CLAUDE.md")
	if err := writeSessionCLAUDEMD(claudeMDPath, planID); err != nil {
		m.markDone(sess, fmt.Errorf("%w: CLAUDE.md: %v", ErrSpawnFailed, err))
		return nil, sess.Err
	}

	// Spawn tmux session.
	model := m.model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// Build the shell command that runs inside tmux.
	// Source tools, then run claude with pipes.
	shellCmd := fmt.Sprintf(
		"source %s && exec claude --model %s --print --output-format stream-json --input-format stream-json --verbose --allowedTools 'Bash(chum-* *),Bash(cat *),Bash(ls *),Bash(grep *),Bash(find *),Read,Glob,Grep' < %s > %s 2>&1",
		toolsPath, model, sess.StdinPath, sess.StdoutPath,
	)

	cmd := exec.Command("tmux", "new-session", "-d",
		"-s", sess.ID,
		"-x", "200", "-y", "50",
		"bash", "-c", shellCmd,
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("CHUM_API_PORT=%s", m.apiPort),
		fmt.Sprintf("CHUM_SESSION_TOKEN=%s", m.token),
	)

	m.logger.Info("Spawning plan session",
		"plan_id", planID,
		"tmux_session", sess.ID,
		"work_dir", workDir,
		"model", model,
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		m.markDone(sess, fmt.Errorf("%w: tmux: %v: %s", ErrSpawnFailed, err, string(out)))
		return nil, sess.Err
	}

	sess.State = StateReady
	return sess, nil
}

// Destroy tears down a plan session.
func (m *Manager) Destroy(planID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[planID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	m.mu.Unlock()

	m.markDone(sess, nil)
	return nil
}

// Reconcile kills all orphaned plan-* tmux sessions on startup.
func (m *Manager) Reconcile() {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		// No tmux server running — nothing to reconcile.
		return
	}

	for _, line := range splitLines(string(out)) {
		if len(line) > 5 && line[:5] == "plan-" {
			m.logger.Info("Reconcile: killing orphaned tmux session", "session", line)
			_ = exec.Command("tmux", "kill-session", "-t", line).Run()
		}
	}
}

// markDone transitions a session to done state and cleans up resources.
func (m *Manager) markDone(sess *Session, err error) {
	sess.State = StateDone
	sess.Err = err

	// Stop the bridge if running.
	if sess.bridge != nil {
		sess.bridge.Stop()
	}

	// Kill tmux session (best-effort).
	_ = exec.Command("tmux", "kill-session", "-t", sess.ID).Run()

	// Clean up pipes.
	if sess.PipeDir != "" {
		_ = os.RemoveAll(sess.PipeDir)
	}

	m.logger.Info("Session destroyed",
		"plan_id", sess.PlanID,
		"error", err,
	)
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a less-random token.
		return "chum-session-token-fallback"
	}
	return fmt.Sprintf("%x", b)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if line != "" {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
