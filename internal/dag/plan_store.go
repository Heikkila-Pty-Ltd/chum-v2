package dag

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

// PlanDoc represents a plan grooming document.
type PlanDoc struct {
	ID           string          `json:"id"`
	Project      string          `json:"project"`
	Title        string          `json:"title"`
	Status       string          `json:"status"`
	SpecJSON     json.RawMessage `json:"spec_json"`
	Conversation json.RawMessage `json:"conversation"`
	DraftTasks   json.RawMessage `json:"draft_tasks"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// PlanDocSummary is a lightweight view for list endpoints (no heavy JSON blobs).
type PlanDocSummary struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ConversationMessage represents a single message in the plan conversation.
type ConversationMessage struct {
	Role      string `json:"role"`      // "user" or "assistant"
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

func generatePlanID() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(99999))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("plan-%05d", n.Int64()), nil
}

// CreatePlan inserts a new plan document.
func (d *DAG) CreatePlan(ctx context.Context, p *PlanDoc) error {
	if p.ID == "" {
		id, err := generatePlanID()
		if err != nil {
			return fmt.Errorf("generate plan id: %w", err)
		}
		p.ID = id
	}
	if p.SpecJSON == nil {
		p.SpecJSON = json.RawMessage("{}")
	}
	if p.Conversation == nil {
		p.Conversation = json.RawMessage("[]")
	}
	if p.DraftTasks == nil {
		p.DraftTasks = json.RawMessage("[]")
	}
	if p.Status == "" {
		p.Status = "draft"
	}

	_, err := d.db.ExecContext(ctx,
		`INSERT INTO plan_docs (id, project, title, status, spec_json, conversation, draft_tasks)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Project, p.Title, p.Status, string(p.SpecJSON), string(p.Conversation), string(p.DraftTasks))
	if err != nil {
		return fmt.Errorf("create plan: %w", err)
	}
	return nil
}

// GetPlan retrieves a plan document by ID.
func (d *DAG) GetPlan(ctx context.Context, id string) (*PlanDoc, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, project, title, status, spec_json, conversation, draft_tasks, created_at, updated_at
		 FROM plan_docs WHERE id = ?`, id)

	var p PlanDoc
	var specJSON, conversation, draftTasks string
	err := row.Scan(&p.ID, &p.Project, &p.Title, &p.Status,
		&specJSON, &conversation, &draftTasks, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	p.SpecJSON = json.RawMessage(specJSON)
	p.Conversation = json.RawMessage(conversation)
	p.DraftTasks = json.RawMessage(draftTasks)
	return &p, nil
}

// ListPlans returns lightweight summaries for a project, ordered by most recent.
func (d *DAG) ListPlans(ctx context.Context, project string) ([]*PlanDocSummary, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, project, title, status, created_at, updated_at
		 FROM plan_docs WHERE project = ? ORDER BY updated_at DESC`, project)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()

	var plans []*PlanDocSummary
	for rows.Next() {
		var p PlanDocSummary
		if err := rows.Scan(&p.ID, &p.Project, &p.Title, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan plan: %w", err)
		}
		plans = append(plans, &p)
	}
	return plans, nil
}

// UpdatePlan updates a plan document's mutable fields.
func (d *DAG) UpdatePlan(ctx context.Context, p *PlanDoc) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE plan_docs SET title = ?, status = ?, spec_json = ?, conversation = ?,
		 draft_tasks = ?, updated_at = datetime('now') WHERE id = ?`,
		p.Title, p.Status, string(p.SpecJSON), string(p.Conversation), string(p.DraftTasks), p.ID)
	if err != nil {
		return fmt.Errorf("update plan: %w", err)
	}
	return nil
}

// AppendConversation adds a message to a plan's conversation within a transaction.
func (d *DAG) AppendConversation(ctx context.Context, planID string, msg ConversationMessage) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var raw string
	err = tx.QueryRowContext(ctx, `SELECT conversation FROM plan_docs WHERE id = ?`, planID).Scan(&raw)
	if err != nil {
		return fmt.Errorf("read conversation: %w", err)
	}

	var conv []ConversationMessage
	if err := json.Unmarshal([]byte(raw), &conv); err != nil {
		return fmt.Errorf("unmarshal conversation: %w", err)
	}

	conv = append(conv, msg)
	updated, err := json.Marshal(conv)
	if err != nil {
		return fmt.Errorf("marshal conversation: %w", err)
	}

	// 500KB cap on conversation.
	if len(updated) > 512000 {
		return fmt.Errorf("conversation exceeds 500KB limit")
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE plan_docs SET conversation = ?, updated_at = datetime('now') WHERE id = ?`,
		string(updated), planID)
	if err != nil {
		return fmt.Errorf("update conversation: %w", err)
	}

	return tx.Commit()
}
