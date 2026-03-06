package dag

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Decision represents a branching moment linked to a task.
// It captures what was considered and why a particular path was chosen.
type Decision struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Title     string    `json:"title"`
	Context   string    `json:"context"` // what triggered the decision
	Outcome   string    `json:"outcome"` // summary of the chosen path
	CreatedAt time.Time `json:"created_at"`
}

// Alternative represents one path considered at a decision point.
type Alternative struct {
	ID         string    `json:"id"`
	DecisionID string    `json:"decision_id"`
	Label      string    `json:"label"`
	Reasoning  string    `json:"reasoning"` // LLM's reasoning for/against
	Selected   bool      `json:"selected"`
	UCTScore   float64   `json:"uct_score"` // optional UCT score (0 if unscored)
	Visits     int       `json:"visits"`    // UCT visit count
	Reward     float64   `json:"reward"`    // UCT cumulative reward
	CreatedAt  time.Time `json:"created_at"`
}

// CreateDecision inserts a decision linked to a task.
func (d *DAG) CreateDecision(ctx context.Context, dec Decision) (string, error) {
	if dec.ID == "" {
		id, err := generateTaskID("dec")
		if err != nil {
			return "", fmt.Errorf("generate decision id: %w", err)
		}
		dec.ID = id
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO decisions (id, task_id, title, context, outcome)
		 VALUES (?, ?, ?, ?, ?)`,
		dec.ID, dec.TaskID, dec.Title, dec.Context, dec.Outcome)
	if err != nil {
		return "", fmt.Errorf("create decision: %w", err)
	}
	return dec.ID, nil
}

// GetDecision returns a single decision by ID.
func (d *DAG) GetDecision(ctx context.Context, id string) (Decision, error) {
	var dec Decision
	err := d.db.QueryRowContext(ctx,
		`SELECT id, task_id, title, context, outcome, created_at
		 FROM decisions WHERE id = ?`, id).
		Scan(&dec.ID, &dec.TaskID, &dec.Title, &dec.Context, &dec.Outcome, &dec.CreatedAt)
	if err != nil {
		return Decision{}, fmt.Errorf("get decision %s: %w", id, err)
	}
	return dec, nil
}

// ListDecisionsForTask returns all decisions linked to a task.
func (d *DAG) ListDecisionsForTask(ctx context.Context, taskID string) ([]Decision, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, task_id, title, context, outcome, created_at
		 FROM decisions WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list decisions for task %s: %w", taskID, err)
	}
	defer rows.Close()
	var decs []Decision
	for rows.Next() {
		var dec Decision
		if err := rows.Scan(&dec.ID, &dec.TaskID, &dec.Title, &dec.Context, &dec.Outcome, &dec.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		decs = append(decs, dec)
	}
	return decs, rows.Err()
}

// CreateAlternative inserts an alternative for a decision.
func (d *DAG) CreateAlternative(ctx context.Context, alt Alternative) (string, error) {
	if alt.ID == "" {
		id, err := generateTaskID("alt")
		if err != nil {
			return "", fmt.Errorf("generate alternative id: %w", err)
		}
		alt.ID = id
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO decision_alternatives
		 (id, decision_id, label, reasoning, selected, uct_score, visits, reward)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		alt.ID, alt.DecisionID, alt.Label, alt.Reasoning,
		alt.Selected, alt.UCTScore, alt.Visits, alt.Reward)
	if err != nil {
		return "", fmt.Errorf("create alternative: %w", err)
	}
	return alt.ID, nil
}

// ListAlternatives returns all alternatives for a decision, ordered by UCT score descending.
func (d *DAG) ListAlternatives(ctx context.Context, decisionID string) ([]Alternative, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, decision_id, label, reasoning, selected, uct_score, visits, reward, created_at
		 FROM decision_alternatives
		 WHERE decision_id = ?
		 ORDER BY uct_score DESC, created_at`, decisionID)
	if err != nil {
		return nil, fmt.Errorf("list alternatives for decision %s: %w", decisionID, err)
	}
	defer rows.Close()
	var alts []Alternative
	for rows.Next() {
		var alt Alternative
		if err := rows.Scan(&alt.ID, &alt.DecisionID, &alt.Label, &alt.Reasoning,
			&alt.Selected, &alt.UCTScore, &alt.Visits, &alt.Reward, &alt.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan alternative: %w", err)
		}
		alts = append(alts, alt)
	}
	return alts, rows.Err()
}

// GetSelectedAlternative returns the chosen alternative for a decision, if any.
func (d *DAG) GetSelectedAlternative(ctx context.Context, decisionID string) (Alternative, error) {
	var alt Alternative
	err := d.db.QueryRowContext(ctx,
		`SELECT id, decision_id, label, reasoning, selected, uct_score, visits, reward, created_at
		 FROM decision_alternatives
		 WHERE decision_id = ? AND selected = 1`, decisionID).
		Scan(&alt.ID, &alt.DecisionID, &alt.Label, &alt.Reasoning,
			&alt.Selected, &alt.UCTScore, &alt.Visits, &alt.Reward, &alt.CreatedAt)
	if err != nil {
		return Alternative{}, fmt.Errorf("get selected alternative for %s: %w", decisionID, err)
	}
	return alt, nil
}

// SelectAlternative marks one alternative as selected and deselects all others
// for the same decision. Also updates the decision's outcome with the alternative label.
func (d *DAG) SelectAlternative(ctx context.Context, decisionID, alternativeID string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Deselect all
	if _, err := tx.ExecContext(ctx,
		"UPDATE decision_alternatives SET selected = 0 WHERE decision_id = ?",
		decisionID); err != nil {
		return fmt.Errorf("deselect alternatives: %w", err)
	}
	// Select the chosen one
	res, err := tx.ExecContext(ctx,
		"UPDATE decision_alternatives SET selected = 1 WHERE id = ? AND decision_id = ?",
		alternativeID, decisionID)
	if err != nil {
		return fmt.Errorf("select alternative: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("alternative %s not found in decision %s", alternativeID, decisionID)
	}

	// Update decision outcome with the selected label
	var label string
	if err := tx.QueryRowContext(ctx,
		"SELECT label FROM decision_alternatives WHERE id = ?",
		alternativeID).Scan(&label); err != nil {
		return fmt.Errorf("read selected label: %w", err)
	}
	if label != "" {
		if _, err := tx.ExecContext(ctx,
			"UPDATE decisions SET outcome = ? WHERE id = ?",
			label, decisionID); err != nil {
			return fmt.Errorf("update decision outcome: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateAlternativeUCT updates the UCT scoring fields for an alternative.
func (d *DAG) UpdateAlternativeUCT(ctx context.Context, id string, score float64, visits int, reward float64) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE decision_alternatives SET uct_score = ?, visits = ?, reward = ? WHERE id = ?`,
		score, visits, reward, id)
	if err != nil {
		return fmt.Errorf("update alternative UCT: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
