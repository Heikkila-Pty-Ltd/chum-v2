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
	ID                 string          `json:"id"`
	Project            string          `json:"project"`
	Title              string          `json:"title"`
	Status             string          `json:"status"`
	SpecJSON           json.RawMessage `json:"spec_json"`
	Conversation       json.RawMessage `json:"conversation"`
	DraftTasks         json.RawMessage `json:"draft_tasks"`
	BriefMarkdown      string          `json:"brief_markdown"`
	WorkingMarkdown    string          `json:"working_markdown"`
	GoalTaskID         string          `json:"goal_task_id"`
	Structured         json.RawMessage `json:"structured"`
	ExecutionBatches   json.RawMessage `json:"execution_batches"`
	MaterializedGoalID string          `json:"materialized_goal_id"`
	NextQuestion       string          `json:"next_question"`
	PlannerReply       string          `json:"planner_reply"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// DraftTask is the parsed form of a single draft task from decomposition.
// Type is "epic", "task", or "subtask". ParentRef links subtasks to their
// parent task or tasks to their epic (using ref, not ID).
type DraftTask struct {
	Ref             string   `json:"ref"`
	Title           string   `json:"title"`
	Type            string   `json:"type,omitempty"` // epic | task | subtask
	Description     string   `json:"description"`
	Acceptance      string   `json:"acceptance"`
	EstimateMinutes int      `json:"estimate_minutes"`
	Batch           int      `json:"batch"`
	DependsOn       []string `json:"depends_on"`
	ParentRef       string   `json:"parent_ref,omitempty"` // ref of parent epic/task
	Children        []string `json:"children,omitempty"`   // refs of child tasks/subtasks
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

	structured := defaultPlanJSON(p.Structured, "{}")
	executionBatches := defaultPlanJSON(p.ExecutionBatches, "[]")

	_, err := d.db.ExecContext(ctx,
		`INSERT INTO plan_docs (id, project, title, status, spec_json, conversation, draft_tasks,
		 brief_markdown, working_markdown, goal_task_id, structured, execution_batches,
		 materialized_goal_id, next_question, planner_reply)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Project, p.Title, p.Status, string(p.SpecJSON), string(p.Conversation), string(p.DraftTasks),
		p.BriefMarkdown, p.WorkingMarkdown, p.GoalTaskID, string(structured), string(executionBatches),
		p.MaterializedGoalID, p.NextQuestion, p.PlannerReply)
	if err != nil {
		return fmt.Errorf("create plan: %w", err)
	}
	return nil
}

func defaultPlanJSON(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}

// GetPlan retrieves a plan document by ID.
func (d *DAG) GetPlan(ctx context.Context, id string) (*PlanDoc, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, project, title, status, spec_json, conversation, draft_tasks,
		 brief_markdown, working_markdown, goal_task_id, structured, execution_batches,
		 materialized_goal_id, next_question, planner_reply, created_at, updated_at
		 FROM plan_docs WHERE id = ?`, id)

	var p PlanDoc
	var specJSON, conversation, draftTasks, structured, executionBatches string
	err := row.Scan(&p.ID, &p.Project, &p.Title, &p.Status,
		&specJSON, &conversation, &draftTasks,
		&p.BriefMarkdown, &p.WorkingMarkdown, &p.GoalTaskID,
		&structured, &executionBatches,
		&p.MaterializedGoalID, &p.NextQuestion, &p.PlannerReply,
		&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	p.SpecJSON = json.RawMessage(specJSON)
	p.Conversation = json.RawMessage(conversation)
	p.DraftTasks = json.RawMessage(draftTasks)
	p.Structured = json.RawMessage(structured)
	p.ExecutionBatches = json.RawMessage(executionBatches)
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

// UpdatePlan updates a plan document's mutable fields (struct-based).
func (d *DAG) UpdatePlan(ctx context.Context, p *PlanDoc) error {
	structured := defaultPlanJSON(p.Structured, "{}")
	executionBatches := defaultPlanJSON(p.ExecutionBatches, "[]")

	_, err := d.db.ExecContext(ctx,
		`UPDATE plan_docs SET title = ?, status = ?, spec_json = ?, conversation = ?,
		 draft_tasks = ?, brief_markdown = ?, working_markdown = ?, goal_task_id = ?,
		 structured = ?, execution_batches = ?, materialized_goal_id = ?,
		 next_question = ?, planner_reply = ?, updated_at = datetime('now') WHERE id = ?`,
		p.Title, p.Status, string(p.SpecJSON), string(p.Conversation), string(p.DraftTasks),
		p.BriefMarkdown, p.WorkingMarkdown, p.GoalTaskID,
		string(structured), string(executionBatches), p.MaterializedGoalID,
		p.NextQuestion, p.PlannerReply, p.ID)
	if err != nil {
		return fmt.Errorf("update plan: %w", err)
	}
	return nil
}

// UpdatePlanFields updates specific fields on a plan document by column name.
func (d *DAG) UpdatePlanFields(ctx context.Context, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]bool{
		"title": true, "status": true, "project": true, "spec_json": true,
		"brief_markdown": true, "working_markdown": true, "goal_task_id": true,
		"conversation": true, "structured": true,
		"draft_tasks": true, "execution_batches": true,
		"materialized_goal_id": true, "next_question": true, "planner_reply": true,
	}
	var setClauses []string
	var args []any
	for k, v := range fields {
		if !allowed[k] {
			return fmt.Errorf("field %q is not updatable on plan_docs", k)
		}
		switch val := v.(type) {
		case json.RawMessage:
			v = string(val)
		case []byte:
			v = string(val)
		}
		setClauses = append(setClauses, k+" = ?")
		args = append(args, v)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)

	query := "UPDATE plan_docs SET " + joinStrings(setClauses, ", ") + " WHERE id = ?"
	res, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update plan fields: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("plan %q not found", id)
	}
	return nil
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
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
