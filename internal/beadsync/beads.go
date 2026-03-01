// Package beadsync coordinates data flow between external sources and the DAG.
package beadsync

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// IssueLister abstracts the beads client for testability.
type IssueLister interface {
	List(ctx context.Context, limit int) ([]beads.Issue, error)
}

// SyncResult summarizes a sync operation.
type SyncResult struct {
	Created int
	Updated int
	Skipped int
	Errors  []string
}

func (r SyncResult) String() string {
	return fmt.Sprintf("created=%d updated=%d skipped=%d errors=%d",
		r.Created, r.Updated, r.Skipped, len(r.Errors))
}

// SyncToDAG reads issues from beads and upserts them into the DAG.
// Only imports issues with status "open" or "ready" — completed/closed issues
// are ignored. This is a one-way sync: beads → DAG.
func SyncToDAG(ctx context.Context, client IssueLister, d *dag.DAG, project string, logger *slog.Logger) (SyncResult, error) {
	issues, err := client.List(ctx, 0) // all issues
	if err != nil {
		return SyncResult{}, fmt.Errorf("bd list: %w", err)
	}

	var result SyncResult
	for _, issue := range issues {
		// Skip completed/closed — we only ingest work that needs doing
		if issue.Status == "closed" || issue.Status == types.StatusCompleted || issue.Status == "done" {
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
				"title":            issue.Title,
				"description":      issue.Description,
				"acceptance":       issue.AcceptanceCriteria,
				"priority":         issue.Priority,
				"estimate_minutes": issue.EstimatedMinutes,
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
		if status == "" || status == types.StatusOpen {
			status = types.StatusOpen // beads "open" → DAG "open" (not yet ready)
		}

		task := dag.Task{
			ID:              issue.ID,
			Title:           issue.Title,
			Description:     buildDescription(issue),
			Status:          status,
			Priority:        issue.Priority,
			Type:            issue.IssueType,
			Labels:          issue.Labels,
			Acceptance:      issue.AcceptanceCriteria,
			EstimateMinutes: issue.EstimatedMinutes,
			Project:         project,
		}
		if _, err := d.CreateTask(ctx, task); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("create %s: %v", issue.ID, err))
			continue
		}
		result.Created++
		logger.Info("Imported task from beads", "id", issue.ID, "title", issue.Title)
	}

	// Import dependency edges — only when both endpoints exist in the DAG.
	// Skipped issues (closed/completed/done) won't be in the DAG, so edges
	// pointing to them would violate foreign key constraints.
	for _, issue := range issues {
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == "" {
				continue
			}
			if _, err := d.GetTask(ctx, issue.ID); err != nil {
				continue // source not in DAG (was skipped)
			}
			if _, err := d.GetTask(ctx, dep.DependsOnID); err != nil {
				continue // target not in DAG (was skipped)
			}
			if err := d.AddEdge(ctx, issue.ID, dep.DependsOnID); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("edge %s→%s: %v", issue.ID, dep.DependsOnID, err))
			}
		}
	}

	return result, nil
}

func buildDescription(issue beads.Issue) string {
	desc := issue.Description
	if issue.Design != "" {
		desc += "\n\nDesign:\n" + issue.Design
	}
	return desc
}
