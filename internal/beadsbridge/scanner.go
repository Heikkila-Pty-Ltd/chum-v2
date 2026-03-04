package beadsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// ScanResult summarizes one project bridge scan cycle.
type ScanResult struct {
	Candidates      int
	GatePassed      int
	Admitted        int
	Updated         int
	Deduped         int
	Skipped         int
	EdgesProjected  int
	EdgesPending    int
	EdgesRejected   int
	DryRun          bool
	Cursor          string
	OutboxProcessed int
}

// Scanner executes beads->CHUM scan/admission cycles for one project.
type Scanner struct {
	DAG    *dag.DAG
	Config config.BeadsBridge
	Logger *slog.Logger
}

// ScanProject runs one scan cycle. In dry-run mode no tasks or edges are mutated.
func (s *Scanner) ScanProject(ctx context.Context, project string, client beads.Store) (ScanResult, error) {
	if s.DAG == nil {
		return ScanResult{}, fmt.Errorf("scanner DAG is nil")
	}
	if client == nil {
		return ScanResult{}, fmt.Errorf("scanner beads client is nil")
	}

	ready, err := client.Ready(ctx, 0)
	if err != nil {
		return ScanResult{}, fmt.Errorf("beads ready for %s: %w", project, err)
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })

	res := ScanResult{Candidates: len(ready), DryRun: s.Config.DryRun}
	fpParts := make([]string, 0, len(ready))

	for _, short := range ready {
		issue := short
		if full, err := client.Show(ctx, short.ID); err == nil {
			issue = full
		}
		fingerprint := FingerprintIssue(issue)
		fpParts = append(fpParts, issue.ID+":"+fingerprint)
		gate := EvaluateGate(issue, s.Config.CanaryLabel)
		if gate.Pass {
			res.GatePassed++
		} else {
			res.Skipped++
		}

		mapRow, mapErr := s.DAG.GetBeadsMappingByIssue(ctx, project, issue.ID)
		mapped := mapErr == nil
		if mapErr != nil && !dag.IsNoRows(mapErr) {
			return res, fmt.Errorf("lookup mapping %s/%s: %w", project, issue.ID, mapErr)
		}

		switch {
		case mapped && mapRow.LastFingerprint == fingerprint:
			res.Deduped++
			_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "scan", "dedupe", "same_fingerprint", fingerprint, map[string]any{
				"dry_run": s.Config.DryRun,
			})
		case mapped:
			if !s.Config.DryRun {
				if err := s.updateMappedTask(ctx, mapRow.TaskID, issue); err != nil {
					return res, fmt.Errorf("update mapped task %s for issue %s: %w", mapRow.TaskID, issue.ID, err)
				}
				if err := s.DAG.UpsertBeadsMapping(ctx, project, issue.ID, mapRow.TaskID, fingerprint); err != nil {
					return res, fmt.Errorf("update mapping fingerprint for %s: %w", issue.ID, err)
				}
			}
			res.Updated++
			_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "scan", "updated", "mapped_issue_changed", fingerprint, map[string]any{
				"dry_run": s.Config.DryRun,
			})
		default:
			if !gate.Pass {
				_ = s.audit(ctx, project, issue.ID, "", "gate", "skip", gate.Reason, fingerprint, map[string]any{
					"dry_run": s.Config.DryRun,
				})
				continue
			}
			taskID := issue.ID
			if s.Config.DryRun {
				_ = s.audit(ctx, project, issue.ID, taskID, "admission", "would_admit", "dry_run", fingerprint, map[string]any{
					"status": issue.Status,
				})
				continue
			}
			if _, err := s.DAG.GetTask(ctx, taskID); err != nil {
				task := dag.Task{
					ID:              taskID,
					Title:           issue.Title,
					Description:     buildDescription(issue),
					Status:          mapIssueStatus(issue.Status),
					Priority:        issue.Priority,
					Type:            issue.IssueType,
					Labels:          issue.Labels,
					Acceptance:      issue.AcceptanceCriteria,
					EstimateMinutes: issue.EstimatedMinutes,
					Project:         project,
					Metadata: map[string]string{
						"beads_issue_id": issue.ID,
						"beads_bridge":   "true",
					},
				}
				if _, createErr := s.DAG.CreateTask(ctx, task); createErr != nil {
					return res, fmt.Errorf("admit issue %s into task DAG: %w", issue.ID, createErr)
				}
			}
			if err := s.DAG.UpsertBeadsMapping(ctx, project, issue.ID, taskID, fingerprint); err != nil {
				return res, fmt.Errorf("persist mapping for admitted issue %s: %w", issue.ID, err)
			}
			res.Admitted++
			_ = s.audit(ctx, project, issue.ID, taskID, "admission", "admitted", "canary_gate_pass", fingerprint, map[string]any{
				"status": issue.Status,
			})
			mapRow = dag.BeadsSyncMapRow{TaskID: taskID}
			mapped = true
		}

		if !mapped || s.Config.DryRun {
			if mapped && s.Config.DryRun {
				_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "dependency_projection", "skip", "dry_run", fingerprint, nil)
			}
			continue
		}

		// S4: dependency projection for mapped issues.
		for _, dep := range issue.Dependencies {
			if strings.TrimSpace(dep.DependsOnID) == "" {
				continue
			}
			targetMap, depErr := s.DAG.GetBeadsMappingByIssue(ctx, project, dep.DependsOnID)
			if depErr != nil {
				if dag.IsNoRows(depErr) {
					res.EdgesPending++
					_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "dependency_projection", "pending", "dependency_unmapped", fingerprint, map[string]any{
						"depends_on_issue_id": dep.DependsOnID,
					})
					continue
				}
				return res, fmt.Errorf("resolve dependency mapping for %s -> %s: %w", issue.ID, dep.DependsOnID, depErr)
			}
			if mapRow.TaskID == targetMap.TaskID {
				res.EdgesRejected++
				_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "dependency_projection", "reject", "self_edge", fingerprint, map[string]any{
					"depends_on_issue_id": dep.DependsOnID,
				})
				continue
			}
			if err := s.DAG.AddEdgeWithSource(ctx, mapRow.TaskID, targetMap.TaskID, "beads_bridge"); err != nil {
				if strings.Contains(err.Error(), "cycle") {
					res.EdgesRejected++
					_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "dependency_projection", "reject", "cycle_detected", fingerprint, map[string]any{
						"depends_on_issue_id": dep.DependsOnID,
						"depends_on_task_id":  targetMap.TaskID,
					})
					continue
				}
				return res, fmt.Errorf("project dependency edge %s -> %s: %w", mapRow.TaskID, targetMap.TaskID, err)
			}
			res.EdgesProjected++
			_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "dependency_projection", "projected", "mapped_dependency", fingerprint, map[string]any{
				"depends_on_issue_id": dep.DependsOnID,
				"depends_on_task_id":  targetMap.TaskID,
			})
		}
	}

	res.Cursor = buildCursor(fpParts)
	if err := s.DAG.UpsertBeadsCursor(ctx, project, res.Cursor, time.Now().UTC()); err != nil {
		return res, fmt.Errorf("persist scan cursor for %s: %w", project, err)
	}
	_ = s.audit(ctx, project, "", "", "scan", "summary", "scan_complete", res.Cursor, map[string]any{
		"candidates":      res.Candidates,
		"gate_passed":     res.GatePassed,
		"admitted":        res.Admitted,
		"updated":         res.Updated,
		"deduped":         res.Deduped,
		"skipped":         res.Skipped,
		"edges_projected": res.EdgesProjected,
		"edges_pending":   res.EdgesPending,
		"edges_rejected":  res.EdgesRejected,
		"dry_run":         res.DryRun,
	})
	return res, nil
}

func (s *Scanner) updateMappedTask(ctx context.Context, taskID string, issue beads.Issue) error {
	return s.DAG.UpdateTask(ctx, taskID, map[string]any{
		"title":            issue.Title,
		"description":      buildDescription(issue),
		"acceptance":       issue.AcceptanceCriteria,
		"priority":         issue.Priority,
		"estimate_minutes": issue.EstimatedMinutes,
		"labels":           issue.Labels,
	})
}

func (s *Scanner) audit(ctx context.Context, project, issueID, taskID, eventKind, decision, reason, fingerprint string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return s.DAG.InsertBeadsAudit(ctx, dag.BeadsSyncAuditRow{
		Project:     project,
		IssueID:     issueID,
		TaskID:      taskID,
		EventKind:   eventKind,
		Decision:    decision,
		Reason:      reason,
		Fingerprint: fingerprint,
		Details:     string(b),
	})
}

func mapIssueStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case string(types.StatusReady):
		return string(types.StatusReady)
	case "in_progress", string(types.StatusRunning):
		return string(types.StatusRunning)
	default:
		return string(types.StatusOpen)
	}
}

func buildDescription(issue beads.Issue) string {
	desc := issue.Description
	if strings.TrimSpace(issue.Design) != "" {
		if strings.TrimSpace(desc) != "" {
			desc += "\n\n"
		}
		desc += "Design:\n" + issue.Design
	}
	return desc
}

func buildCursor(parts []string) string {
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
