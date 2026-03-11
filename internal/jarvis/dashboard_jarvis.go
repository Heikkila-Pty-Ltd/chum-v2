package jarvis

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	_ "modernc.org/sqlite" // register sqlite driver
)

var jarvisDBMu sync.Mutex

// openJarvisDB returns the cached connection to the Jarvis KB, retrying on transient failures.
func (a *API) openJarvisDB() (*sql.DB, error) {
	jarvisDBMu.Lock()
	defer jarvisDBMu.Unlock()

	if a.jarvisDB != nil {
		return a.jarvisDB, nil
	}

	dsn := a.JarvisKBPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open jarvis KB: %w", err)
	}
	db.SetMaxOpenConns(2)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping jarvis KB: %w", err)
	}
	a.jarvisDB = db
	return db, nil
}

// --- /api/dashboard/jarvis/actions ---

type jarvisAction struct {
	Type      string `json:"type"`    // "blocked_goal", "pending_action", "recurring_failure"
	Urgency   string `json:"urgency"` // "high", "medium", "low"
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	GoalID    int    `json:"goal_id,omitempty"`
	SetAt     string `json:"set_at,omitempty"`
	FailCount int    `json:"fail_count,omitempty"`
}

func (a *API) handleJarvisActions(w http.ResponseWriter, _ *http.Request) {
	db, err := a.openJarvisDB()
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var actions []jarvisAction

	// 1. Blocked goals — high urgency.
	rows, err := db.Query(`
		SELECT id, title, blocked_reason, priority
		FROM goals
		WHERE status = 'active' AND blocked_reason != ''
		ORDER BY priority DESC
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, priority int
			var title, reason string
			if rows.Scan(&id, &title, &reason, &priority) == nil {
				actions = append(actions, jarvisAction{
					Type:    "blocked_goal",
					Urgency: "high",
					Title:   title,
					Detail:  reason,
					GoalID:  id,
				})
			}
		}
	}

	// 2. Pending next action from agent_state.
	var pendingAction, pendingSetAt string
	db.QueryRow("SELECT COALESCE(value,'') FROM agent_state WHERE key='pending_next_action'").Scan(&pendingAction)
	db.QueryRow("SELECT COALESCE(value,'') FROM agent_state WHERE key='pending_next_set_at'").Scan(&pendingSetAt)
	if pendingAction != "" {
		actions = append(actions, jarvisAction{
			Type:    "pending_action",
			Urgency: "high",
			Title:   "Jarvis needs you to do something",
			Detail:  pendingAction,
			SetAt:   pendingSetAt,
		})
	}

	// 3. Recurring failure patterns (last 48h, grouped by summary, >=3 occurrences).
	failRows, err := db.Query(`
		SELECT summary, COUNT(*) as cnt
		FROM initiatives
		WHERE outcome = 'failure'
		  AND started_at > datetime('now', '-48 hours')
		GROUP BY summary
		HAVING cnt >= 3
		ORDER BY cnt DESC
		LIMIT 5
	`)
	if err == nil {
		defer failRows.Close()
		for failRows.Next() {
			var summary string
			var cnt int
			if failRows.Scan(&summary, &cnt) == nil {
				actions = append(actions, jarvisAction{
					Type:      "recurring_failure",
					Urgency:   "medium",
					Title:     "Recurring failure",
					Detail:    summary,
					FailCount: cnt,
				})
			}
		}
	}

	if actions == nil {
		actions = []jarvisAction{}
	}
	a.jsonOK(w, actions)
}

// --- POST /api/dashboard/jarvis/actions/resolve ---

type resolveRequest struct {
	Type    string `json:"type"`    // "blocked_goal", "pending_action", "recurring_failure"
	GoalID  int    `json:"goal_id"` // for blocked_goal
	Detail  string `json:"detail"`  // for recurring_failure (the summary text to dismiss)
	Comment string `json:"comment"` // optional user comment
}

func (a *API) handleJarvisResolve(w http.ResponseWriter, r *http.Request) {
	db, err := a.openJarvisDB()
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req resolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	switch req.Type {
	case "blocked_goal":
		if req.GoalID == 0 {
			a.jsonError(w, "goal_id required", http.StatusBadRequest)
			return
		}
		_, err = db.Exec("UPDATE goals SET blocked_reason = '', updated_at = datetime('now') WHERE id = ?", req.GoalID)

	case "pending_action":
		_, err = db.Exec("UPDATE agent_state SET value = '', updated_at = datetime('now') WHERE key = 'pending_next_action'")

	case "recurring_failure":
		// No DB change — frontend handles dismissal via localStorage.
		// We just log the acknowledgement below.
		err = nil

	default:
		a.jsonError(w, "unknown action type", http.StatusBadRequest)
		return
	}

	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Log to context_entries so Jarvis can see what Simon resolved.
	comment := req.Comment
	if comment == "" {
		comment = "(no comment)"
	}
	content := fmt.Sprintf("[dashboard] Resolved %s: %s", req.Type, comment)
	if req.GoalID > 0 {
		content = fmt.Sprintf("[dashboard] Resolved %s (goal #%d): %s", req.Type, req.GoalID, comment)
	}
	if req.Detail != "" && req.Type == "recurring_failure" {
		content = fmt.Sprintf("[dashboard] Dismissed recurring failure %q: %s", req.Detail, comment)
	}
	db.Exec(`INSERT INTO context_entries (entry_type, sender, content, response, entities, topics, created_at)
		VALUES ('feedback', 'simon-dashboard', ?, '', '[]', '[]', datetime('now'))`, content)

	a.jsonOK(w, map[string]string{"status": "ok"})
}

// --- /api/dashboard/jarvis/summary ---

type jarvisSummary struct {
	ActiveGoals    int    `json:"active_goals"`
	TotalFacts     int    `json:"total_facts"`
	RecentOutcomes int    `json:"recent_outcomes"`
	CycleCounter   string `json:"cycle_counter"`
	CurrentFocus   string `json:"current_focus"`
}

func (a *API) handleJarvisSummary(w http.ResponseWriter, _ *http.Request) {
	db, err := a.openJarvisDB()
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var s jarvisSummary
	db.QueryRow("SELECT COUNT(*) FROM goals WHERE status='active'").Scan(&s.ActiveGoals)
	db.QueryRow("SELECT COUNT(*) FROM facts WHERE active=1").Scan(&s.TotalFacts)
	db.QueryRow("SELECT COUNT(*) FROM initiatives WHERE started_at > datetime('now', '-24 hours')").Scan(&s.RecentOutcomes)
	db.QueryRow("SELECT COALESCE(value,'') FROM agent_state WHERE key='cycle_counter'").Scan(&s.CycleCounter)
	db.QueryRow("SELECT COALESCE(value,'') FROM agent_state WHERE key='current_focus'").Scan(&s.CurrentFocus)

	a.jsonOK(w, s)
}

// --- /api/dashboard/jarvis/goals ---

type jarvisGoal struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Status        string  `json:"status"`
	Priority      int     `json:"priority"`
	Category      string  `json:"category"`
	Progress      float64 `json:"progress"`
	BlockedReason string  `json:"blocked_reason"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func (a *API) handleJarvisGoals(w http.ResponseWriter, _ *http.Request) {
	db, err := a.openJarvisDB()
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := db.Query(`
		SELECT id, title, description, status, priority, category, progress, blocked_reason, created_at, updated_at
		FROM goals
		WHERE status IN ('active', 'paused')
		ORDER BY priority DESC, updated_at DESC
	`)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var goals []jarvisGoal
	for rows.Next() {
		var g jarvisGoal
		if err := rows.Scan(&g.ID, &g.Title, &g.Description, &g.Status, &g.Priority, &g.Category, &g.Progress, &g.BlockedReason, &g.CreatedAt, &g.UpdatedAt); err != nil {
			continue
		}
		goals = append(goals, g)
	}
	if goals == nil {
		goals = []jarvisGoal{}
	}
	a.jsonOK(w, goals)
}

// --- /api/dashboard/jarvis/facts ---

type jarvisFact struct {
	ID         int     `json:"id"`
	Category   string  `json:"category"`
	Subject    string  `json:"subject"`
	Fact       string  `json:"fact"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

func (a *API) handleJarvisFacts(w http.ResponseWriter, r *http.Request) {
	db, err := a.openJarvisDB()
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	category := r.URL.Query().Get("category")
	var rows *sql.Rows
	if category != "" {
		rows, err = db.Query(`
			SELECT id, category, subject, fact, source, confidence, created_at, updated_at
			FROM facts WHERE active=1 AND category=?
			ORDER BY updated_at DESC
		`, category)
	} else {
		rows, err = db.Query(`
			SELECT id, category, subject, fact, source, confidence, created_at, updated_at
			FROM facts WHERE active=1
			ORDER BY category, updated_at DESC
		`)
	}
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var facts []jarvisFact
	for rows.Next() {
		var f jarvisFact
		if err := rows.Scan(&f.ID, &f.Category, &f.Subject, &f.Fact, &f.Source, &f.Confidence, &f.CreatedAt, &f.UpdatedAt); err != nil {
			continue
		}
		facts = append(facts, f)
	}
	if facts == nil {
		facts = []jarvisFact{}
	}
	a.jsonOK(w, facts)
}

// --- /api/dashboard/jarvis/initiatives ---

type jarvisInitiative struct {
	ID          int     `json:"id"`
	Summary     string  `json:"summary"`
	GoalID      *int    `json:"goal_id"`
	ActionTaken string  `json:"action_taken"`
	Outcome     string  `json:"outcome"`
	Impact      string  `json:"impact_assessment"`
	StartedAt   string  `json:"started_at"`
	CompletedAt string  `json:"completed_at"`
	DurationS   float64 `json:"duration_s"`
}

func (a *API) handleJarvisInitiatives(w http.ResponseWriter, r *http.Request) {
	db, err := a.openJarvisDB()
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	rows, err := db.Query(`
		SELECT id, summary, goal_id, action_taken, outcome, impact_assessment, started_at, completed_at, duration_s
		FROM initiatives
		ORDER BY started_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var initiatives []jarvisInitiative
	for rows.Next() {
		var i jarvisInitiative
		if err := rows.Scan(&i.ID, &i.Summary, &i.GoalID, &i.ActionTaken, &i.Outcome, &i.Impact, &i.StartedAt, &i.CompletedAt, &i.DurationS); err != nil {
			continue
		}
		initiatives = append(initiatives, i)
	}
	if initiatives == nil {
		initiatives = []jarvisInitiative{}
	}
	a.jsonOK(w, initiatives)
}

// --- /api/dashboard/jarvis/state ---

type jarvisStateEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

func (a *API) handleJarvisState(w http.ResponseWriter, _ *http.Request) {
	db, err := a.openJarvisDB()
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := db.Query("SELECT key, value, updated_at FROM agent_state ORDER BY key")
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var entries []jarvisStateEntry
	for rows.Next() {
		var e jarvisStateEntry
		if err := rows.Scan(&e.Key, &e.Value, &e.UpdatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []jarvisStateEntry{}
	}
	a.jsonOK(w, entries)
}
