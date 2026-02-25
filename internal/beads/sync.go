package beads

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Heikkila-Pty-Ltd/chum/internal/dag"
)

// SyncResult summarizes a sync operation.
type SyncResult struct {
	Created  int
	Updated  int
	Skipped  int
	Errors   []string
}

func (r SyncResult) String() string {
	return fmt.Sprintf("created=%d updated=%d skipped=%d errors=%d",
		r.Created, r.Updated, r.Skipped, len(r.Errors))
}

// SyncToDAG reads issues from beads and upserts them into the DAG.
// Only imports issues with status "open" or "ready" — completed/closed issues
// are ignored. This is a one-way sync: beads → DAG.
func SyncToDAG(ctx context.Context, client *Client, d *dag.DAG, project string, logger *slog.Logger) (SyncResult, error) {
	issues, err := client.List(ctx, 0) // all issues
	if err != nil {
		return SyncResult{}, fmt.Errorf("bd list: %w", err)
	}

	var result SyncResult
	for _, issue := range issues {
		// Skip completed/closed — we only ingest work that needs doing
		if issue.Status == "closed" || issue.Status == "completed" || issue.Status == "done" {
			result.Skipped++
			continue
		}

		// Check if task already exists in the DAG
		existing, err := d.GetTask(ctx, issue.ID)
		if err == nil {
			// Task exists — update if beads version is different
			if existing.Title == issue.Title &&
				existing.Description == issue.Description &&
				existing.Acceptance == issue.AcceptanceCriteria {
				result.Skipped++
				continue
			}
			// Update changed fields
			fields := map[string]any{
				"title":       issue.Title,
				"description": issue.Description,
				"acceptance":  issue.AcceptanceCriteria,
				"priority":    issue.Priority,
			}
			if issue.Labels != nil {
				fields["labels"] = issue.Labels
			}
			if err := d.UpdateTask(ctx, issue.ID, fields); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("update %s: %v", issue.ID, err))
				continue
			}
			result.Updated++
			logger.Info("Updated task from beads", "id", issue.ID, "title", issue.Title)
			continue
		}

		// Create new task
		status := issue.Status
		if status == "" || status == "open" {
			status = "open" // beads "open" → DAG "open" (not yet ready)
		}

		task := dag.Task{
			ID:          issue.ID,
			Title:       issue.Title,
			Description: buildDescription(issue),
			Status:      status,
			Priority:    issue.Priority,
			Type:        issue.IssueType,
			Labels:      issue.Labels,
			Acceptance:  issue.AcceptanceCriteria,
			Project:     project,
		}
		if _, err := d.CreateTask(ctx, task); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("create %s: %v", issue.ID, err))
			continue
		}
		result.Created++
		logger.Info("Imported task from beads", "id", issue.ID, "title", issue.Title)
	}

	// Import dependency edges
	for _, issue := range issues {
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID != "" {
				// issue depends on dep.DependsOnID
				_ = d.AddEdge(ctx, issue.ID, dep.DependsOnID)
			}
		}
	}

	return result, nil
}

func buildDescription(issue Issue) string {
	desc := issue.Description
	if issue.Design != "" {
		desc += "\n\nDesign:\n" + issue.Design
	}
	return desc
}
