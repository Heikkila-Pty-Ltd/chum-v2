package jarvis

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

// setupJarvisTestDB creates a temp Jarvis KB with schema and seed data.
func setupJarvisTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "jarvis_test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE goals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			priority INTEGER NOT NULL DEFAULT 5,
			category TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '[]',
			progress REAL NOT NULL DEFAULT 0.0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			completed_at TEXT NOT NULL DEFAULT '',
			blocked_reason TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE facts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			category TEXT NOT NULL DEFAULT 'general',
			subject TEXT NOT NULL DEFAULT '',
			fact TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 1.0,
			active INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE initiatives (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			summary TEXT NOT NULL DEFAULT '',
			goal_id INTEGER,
			capability_ids TEXT NOT NULL DEFAULT '[]',
			action_taken TEXT NOT NULL DEFAULT '',
			outcome TEXT NOT NULL DEFAULT 'pending',
			impact_assessment TEXT NOT NULL DEFAULT '',
			morsels_created TEXT NOT NULL DEFAULT '[]',
			schedules_created TEXT NOT NULL DEFAULT '[]',
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			completed_at TEXT NOT NULL DEFAULT '',
			duration_s REAL NOT NULL DEFAULT 0.0
		)`,
		`CREATE TABLE agent_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE context_entries (
			id INTEGER PRIMARY KEY,
			entry_type TEXT NOT NULL DEFAULT '',
			sender TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL DEFAULT '',
			response TEXT NOT NULL DEFAULT '',
			entities TEXT NOT NULL DEFAULT '[]',
			topics TEXT NOT NULL DEFAULT '[]',
			goal_id INTEGER,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		// Seed data.
		`INSERT INTO goals (title, status, priority, category, progress, blocked_reason) VALUES
			('Goal A', 'active', 10, 'dev', 50.0, 'needs approval'),
			('Goal B', 'active', 5, 'ops', 80.0, ''),
			('Goal C', 'completed', 3, 'dev', 100.0, '')`,
		`INSERT INTO facts (category, subject, fact, active) VALUES
			('directive', 'style', 'no markdown', 1),
			('preference', 'tone', 'be direct', 1),
			('general', 'old', 'outdated', 0)`,
		`INSERT INTO initiatives (summary, goal_id, outcome, duration_s, started_at) VALUES
			('Did thing A', 1, 'success', 10.0, datetime('now')),
			('Did thing B', 1, 'failure', 5.0, datetime('now')),
			('Did thing B', NULL, 'failure', 5.0, datetime('now')),
			('Did thing B', NULL, 'failure', 5.0, datetime('now'))`,
		`INSERT INTO agent_state (key, value) VALUES
			('cycle_counter', '42'),
			('current_focus', 'testing'),
			('pending_next_action', 'do the thing'),
			('pending_next_set_at', '2026-01-01 00:00:00')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup SQL %q: %v", s[:40], err)
		}
	}
	return dbPath
}

func jarvisTestAPI(t *testing.T) *API {
	t.Helper()
	dbPath := setupJarvisTestDB(t)
	// Reset the sync.Once so each test gets a fresh connection.
	jarvisDBOnce = sync.Once{}
	return &API{
		JarvisKBPath: dbPath,
		Logger:       testLogger(),
	}
}

func TestJarvisSummary(t *testing.T) {
	api := jarvisTestAPI(t)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/jarvis/summary", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	var s jarvisSummary
	json.NewDecoder(w.Body).Decode(&s)
	if s.ActiveGoals != 2 {
		t.Errorf("active_goals = %d, want 2", s.ActiveGoals)
	}
	if s.TotalFacts != 2 {
		t.Errorf("total_facts = %d, want 2", s.TotalFacts)
	}
	if s.CycleCounter != "42" {
		t.Errorf("cycle_counter = %q, want 42", s.CycleCounter)
	}
	if s.CurrentFocus != "testing" {
		t.Errorf("current_focus = %q, want testing", s.CurrentFocus)
	}
}

func TestJarvisGoals(t *testing.T) {
	api := jarvisTestAPI(t)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/jarvis/goals", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var goals []jarvisGoal
	json.NewDecoder(w.Body).Decode(&goals)
	if len(goals) != 2 {
		t.Fatalf("got %d goals, want 2 (active only)", len(goals))
	}
	// Sorted by priority DESC.
	if goals[0].Priority != 10 {
		t.Errorf("first goal priority = %d, want 10", goals[0].Priority)
	}
}

func TestJarvisFacts(t *testing.T) {
	api := jarvisTestAPI(t)

	// All active facts.
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/jarvis/facts", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var facts []jarvisFact
	json.NewDecoder(w.Body).Decode(&facts)
	if len(facts) != 2 {
		t.Fatalf("got %d facts, want 2 (active only)", len(facts))
	}

	// Filter by category.
	w2 := httptest.NewRecorder()
	api.Handler().ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/dashboard/jarvis/facts?category=directive", nil))
	var filtered []jarvisFact
	json.NewDecoder(w2.Body).Decode(&filtered)
	if len(filtered) != 1 {
		t.Errorf("got %d directive facts, want 1", len(filtered))
	}
}

func TestJarvisInitiatives(t *testing.T) {
	api := jarvisTestAPI(t)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/jarvis/initiatives?limit=2", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var inits []jarvisInitiative
	json.NewDecoder(w.Body).Decode(&inits)
	if len(inits) != 2 {
		t.Errorf("got %d initiatives, want 2 (limit)", len(inits))
	}
}

func TestJarvisState(t *testing.T) {
	api := jarvisTestAPI(t)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/jarvis/state", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var entries []jarvisStateEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 4 {
		t.Errorf("got %d state entries, want 4", len(entries))
	}
}

func TestJarvisActions(t *testing.T) {
	api := jarvisTestAPI(t)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/jarvis/actions", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	var actions []jarvisAction
	json.NewDecoder(w.Body).Decode(&actions)

	// Expect: 1 blocked goal + 1 pending action + 1 recurring failure (3x "Did thing B").
	var blocked, pending, recurring int
	for _, a := range actions {
		switch a.Type {
		case "blocked_goal":
			blocked++
			if a.GoalID == 0 {
				t.Error("blocked_goal has no goal_id")
			}
		case "pending_action":
			pending++
		case "recurring_failure":
			recurring++
			if a.FailCount < 3 {
				t.Errorf("recurring failure count = %d, want >= 3", a.FailCount)
			}
		}
	}
	if blocked != 1 {
		t.Errorf("blocked_goal actions = %d, want 1", blocked)
	}
	if pending != 1 {
		t.Errorf("pending_action actions = %d, want 1", pending)
	}
	if recurring != 1 {
		t.Errorf("recurring_failure actions = %d, want 1", recurring)
	}
}

func TestJarvisResolveBlockedGoal(t *testing.T) {
	api := jarvisTestAPI(t)
	body := `{"type":"blocked_goal","goal_id":1,"comment":"approved it"}`
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/jarvis/actions/resolve", strings.NewReader(body)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}

	// Verify blocked_reason was cleared.
	db, _ := api.openJarvisDB()
	var reason string
	db.QueryRow("SELECT blocked_reason FROM goals WHERE id=1").Scan(&reason)
	if reason != "" {
		t.Errorf("blocked_reason = %q, want empty", reason)
	}

	// Verify context_entries log.
	var content string
	db.QueryRow("SELECT content FROM context_entries ORDER BY rowid DESC LIMIT 1").Scan(&content)
	if !strings.Contains(content, "approved it") {
		t.Errorf("context_entries content = %q, want to contain comment", content)
	}
}

func TestJarvisResolvePendingAction(t *testing.T) {
	api := jarvisTestAPI(t)
	body := `{"type":"pending_action","comment":"done"}`
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/jarvis/actions/resolve", strings.NewReader(body)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}

	db, _ := api.openJarvisDB()
	var val string
	db.QueryRow("SELECT value FROM agent_state WHERE key='pending_next_action'").Scan(&val)
	if val != "" {
		t.Errorf("pending_next_action = %q, want empty", val)
	}
}

func TestJarvisEndpointsDisabledWithoutPath(t *testing.T) {
	// When JarvisKBPath is empty, endpoints should 404.
	api := &API{Logger: testLogger()}
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/jarvis/summary", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when JarvisKBPath is empty", w.Code)
	}
}

func TestMain(m *testing.M) {
	// Ensure we don't pollute global state across test files.
	os.Exit(m.Run())
}
