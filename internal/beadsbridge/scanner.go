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
	EdgesPruned     int
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

	issueByID := make(map[string]beads.Issue)
	allIssues, listErr := client.List(ctx, 0)
	if listErr != nil {
		if s.Logger != nil {
			s.Logger.Warn("Failed to list all beads issues; falling back to per-issue show", "project", project, "error", listErr)
		}
	} else {
		for _, issue := range allIssues {
			issueByID[issue.ID] = issue
		}

		reopened, unblockErr := s.unblockStaleBlockedIssues(ctx, project, client, issueByID)
		if unblockErr != nil {
			return ScanResult{}, fmt.Errorf("unblock stale blocked issues for %s: %w", project, unblockErr)
		}
		if reopened > 0 && s.Logger != nil {
			s.Logger.Info("Beads bridge reopened stale blocked issues", "project", project, "count", reopened)
		}
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
		if full, ok := issueByID[short.ID]; ok {
			issue = full
		} else if full, err := client.Show(ctx, short.ID); err == nil {
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
			if !s.Config.DryRun {
				if err := s.promoteMappedTaskFromReady(ctx, mapRow.TaskID); err != nil {
					return res, fmt.Errorf("promote mapped task %s for ready issue %s: %w", mapRow.TaskID, issue.ID, err)
				}
			}
			res.Deduped++
			_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "scan", "dedupe", "same_fingerprint", fingerprint, map[string]any{
				"dry_run": s.Config.DryRun,
			})
		case mapped:
			if !s.Config.DryRun {
				if err := s.updateMappedTask(ctx, mapRow.TaskID, issue); err != nil {
					return res, fmt.Errorf("update mapped task %s for issue %s: %w", mapRow.TaskID, issue.ID, err)
				}
				if err := s.promoteMappedTaskFromReady(ctx, mapRow.TaskID); err != nil {
					return res, fmt.Errorf("promote mapped task %s for ready issue %s: %w", mapRow.TaskID, issue.ID, err)
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
					Status:          mapReadyIssueStatus(issue.Status),
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

		if err := s.syncTerminalDependencyStatuses(ctx, project, issue, issueByID, client); err != nil {
			return res, fmt.Errorf("sync terminal dependency statuses for %s: %w", issue.ID, err)
		}

		if !mapped || s.Config.DryRun {
			if mapped && s.Config.DryRun {
				_ = s.audit(ctx, project, issue.ID, mapRow.TaskID, "dependency_projection", "skip", "dry_run", fingerprint, nil)
			}
			continue
		}

		// S4: dependency projection for mapped issues.
		if err := s.syncProjectedDependencies(ctx, project, issue, mapRow.TaskID, fingerprint, &res); err != nil {
			return res, fmt.Errorf("sync projected dependencies for %s: %w", issue.ID, err)
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
		"edges_pruned":    res.EdgesPruned,
		"edges_pending":   res.EdgesPending,
		"edges_rejected":  res.EdgesRejected,
		"dry_run":         res.DryRun,
	})
	return res, nil
}

func (s *Scanner) unblockStaleBlockedIssues(
	ctx context.Context,
	project string,
	client beads.Store,
	issueByID map[string]beads.Issue,
) (int, error) {
	if len(issueByID) == 0 {
		return 0, nil
	}

	ids := make([]string, 0, len(issueByID))
	for id := range issueByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	reopened := 0
	for _, id := range ids {
		issue := issueByID[id]
		if !isBlockedIssueStatus(issue.Status) || len(issue.Dependencies) == 0 {
			continue
		}

		allDepsTerminal := true
		for _, dep := range issue.Dependencies {
			depIssueID := strings.TrimSpace(dep.DependsOnID)
			if depIssueID == "" {
				continue
			}
			depIssue, ok := issueByID[depIssueID]
			if !ok {
				var err error
				depIssue, err = client.Show(ctx, depIssueID)
				if err != nil {
					allDepsTerminal = false
					break
				}
				issueByID[depIssueID] = depIssue
			}
			if !isIssueTerminalStatus(depIssue.Status) {
				allDepsTerminal = false
				break
			}
		}

		if !allDepsTerminal {
			continue
		}
		if s.Config.DryRun {
			continue
		}

		if err := client.Update(ctx, issue.ID, map[string]string{"status": "open"}); err != nil {
			return reopened, fmt.Errorf("set issue %s status open: %w", issue.ID, err)
		}
		issue.Status = "open"
		issueByID[issue.ID] = issue
		reopened++
	}

	return reopened, nil
}

func (s *Scanner) syncProjectedDependencies(
	ctx context.Context,
	project string,
	issue beads.Issue,
	taskID string,
	fingerprint string,
	res *ScanResult,
) error {
	desiredByIssue := make(map[string]string) // dep issue id -> dep task id
	desiredByTask := make(map[string]string)  // dep task id -> dep issue id
	pendingIssueDeps := make(map[string]bool)

	for _, dep := range issue.Dependencies {
		depIssueID := strings.TrimSpace(dep.DependsOnID)
		if depIssueID == "" {
			continue
		}
		if _, seen := desiredByIssue[depIssueID]; seen {
			continue
		}

		targetMap, depErr := s.DAG.GetBeadsMappingByIssue(ctx, project, depIssueID)
		if depErr != nil {
			if dag.IsNoRows(depErr) {
				pendingIssueDeps[depIssueID] = true
				res.EdgesPending++
				_ = s.audit(ctx, project, issue.ID, taskID, "dependency_projection", "pending", "dependency_unmapped", fingerprint, map[string]any{
					"depends_on_issue_id": depIssueID,
				})
				continue
			}
			return fmt.Errorf("resolve dependency mapping for %s -> %s: %w", issue.ID, depIssueID, depErr)
		}
		if taskID == targetMap.TaskID {
			res.EdgesRejected++
			_ = s.audit(ctx, project, issue.ID, taskID, "dependency_projection", "reject", "self_edge", fingerprint, map[string]any{
				"depends_on_issue_id": depIssueID,
			})
			continue
		}
		desiredByIssue[depIssueID] = targetMap.TaskID
		desiredByTask[targetMap.TaskID] = depIssueID
	}

	existingDeps, err := s.DAG.GetDependencies(ctx, taskID)
	if err != nil {
		return fmt.Errorf("list existing deps for %s: %w", taskID, err)
	}
	existingSources := make(map[string]string, len(existingDeps)) // dep task id -> normalized source
	for _, depTaskID := range existingDeps {
		source, sourceErr := s.DAG.GetEdgeSource(ctx, taskID, depTaskID)
		if sourceErr != nil {
			return fmt.Errorf("lookup edge source %s -> %s: %w", taskID, depTaskID, sourceErr)
		}
		existingSources[depTaskID] = strings.ToLower(strings.TrimSpace(source))
	}

	// Prune stale bridge-projected edges that no longer exist in beads.
	for depTaskID, source := range existingSources {
		if !isBridgeDependencySource(source) {
			continue
		}
		if _, keep := desiredByTask[depTaskID]; keep {
			continue
		}

		depIssueID := ""
		if depMap, depMapErr := s.DAG.GetBeadsMappingByTask(ctx, project, depTaskID); depMapErr == nil {
			depIssueID = depMap.IssueID
		} else if !dag.IsNoRows(depMapErr) {
			return fmt.Errorf("resolve dependency task mapping %s: %w", depTaskID, depMapErr)
		} else if len(pendingIssueDeps) > 0 {
			// Keep legacy bridge edges in place while dependency mappings are still pending.
			continue
		}

		if err := s.DAG.RemoveEdge(ctx, taskID, depTaskID); err != nil {
			return fmt.Errorf("remove stale dependency edge %s -> %s: %w", taskID, depTaskID, err)
		}
		delete(existingSources, depTaskID)
		res.EdgesPruned++
		_ = s.audit(ctx, project, issue.ID, taskID, "dependency_projection", "pruned", "stale_dependency", fingerprint, map[string]any{
			"depends_on_issue_id": depIssueID,
			"depends_on_task_id":  depTaskID,
			"edge_source":         source,
		})
	}

	seenDesiredIssue := make(map[string]bool, len(issue.Dependencies))
	for _, dep := range issue.Dependencies {
		depIssueID := strings.TrimSpace(dep.DependsOnID)
		if depIssueID == "" || seenDesiredIssue[depIssueID] {
			continue
		}
		seenDesiredIssue[depIssueID] = true

		depTaskID, ok := desiredByIssue[depIssueID]
		if !ok {
			continue
		}

		if source, exists := existingSources[depTaskID]; exists && !isBridgeDependencySource(source) {
			// Replace stale non-bridge edge source (e.g. AST fence) with bridge source.
			if err := s.DAG.RemoveEdge(ctx, taskID, depTaskID); err != nil {
				return fmt.Errorf("replace dependency edge source %s -> %s: %w", taskID, depTaskID, err)
			}
			delete(existingSources, depTaskID)
			res.EdgesPruned++
			_ = s.audit(ctx, project, issue.ID, taskID, "dependency_projection", "pruned", "replace_non_bridge_source", fingerprint, map[string]any{
				"depends_on_issue_id": depIssueID,
				"depends_on_task_id":  depTaskID,
				"edge_source":         source,
			})
		}

		if source, exists := existingSources[depTaskID]; exists && isBridgeDependencySource(source) {
			continue
		}

		if err := s.DAG.AddEdgeWithSource(ctx, taskID, depTaskID, "beads_bridge"); err != nil {
			if strings.Contains(err.Error(), "cycle") {
				res.EdgesRejected++
				_ = s.audit(ctx, project, issue.ID, taskID, "dependency_projection", "reject", "cycle_detected", fingerprint, map[string]any{
					"depends_on_issue_id": depIssueID,
					"depends_on_task_id":  depTaskID,
				})
				continue
			}
			return fmt.Errorf("project dependency edge %s -> %s: %w", taskID, depTaskID, err)
		}
		existingSources[depTaskID] = "beads_bridge"
		res.EdgesProjected++
		_ = s.audit(ctx, project, issue.ID, taskID, "dependency_projection", "projected", "mapped_dependency", fingerprint, map[string]any{
			"depends_on_issue_id": depIssueID,
			"depends_on_task_id":  depTaskID,
		})
	}

	return nil
}

func isBridgeDependencySource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "beads", "beads_bridge":
		return true
	default:
		return false
	}
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

func (s *Scanner) promoteMappedTaskFromReady(ctx context.Context, taskID string) error {
	task, err := s.DAG.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(task.Status)) {
	case string(types.StatusReady), string(types.StatusRunning), string(types.StatusCompleted), string(types.StatusDone):
		return nil
	default:
		return s.DAG.UpdateTaskStatus(ctx, taskID, string(types.StatusReady))
	}
}

func (s *Scanner) syncTerminalDependencyStatuses(ctx context.Context, project string, issue beads.Issue, issueByID map[string]beads.Issue, client beads.Store) error {
	if s.Config.DryRun {
		return nil
	}
	for _, dep := range issue.Dependencies {
		depIssueID := strings.TrimSpace(dep.DependsOnID)
		if depIssueID == "" {
			continue
		}
		depIssue, ok := issueByID[depIssueID]
		if !ok {
			var err error
			depIssue, err = client.Show(ctx, depIssueID)
			if err != nil {
				continue
			}
		}
		desiredStatus := mapIssueStatus(depIssue.Status)
		if !isTerminalTaskStatus(desiredStatus) {
			continue
		}
		depMap, mapErr := s.DAG.GetBeadsMappingByIssue(ctx, project, depIssueID)
		if mapErr != nil {
			if dag.IsNoRows(mapErr) {
				continue
			}
			return mapErr
		}
		depTask, taskErr := s.DAG.GetTask(ctx, depMap.TaskID)
		if taskErr != nil {
			return taskErr
		}
		current := strings.ToLower(strings.TrimSpace(depTask.Status))
		if current == strings.ToLower(desiredStatus) || current == string(types.StatusRunning) || isTerminalTaskStatus(depTask.Status) {
			continue
		}
		if err := s.DAG.UpdateTaskStatus(ctx, depMap.TaskID, desiredStatus); err != nil {
			return err
		}
	}
	return nil
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
	case "closed", string(types.StatusCompleted), string(types.StatusDone):
		return string(types.StatusCompleted)
	default:
		return string(types.StatusOpen)
	}
}

func mapReadyIssueStatus(status string) string {
	mapped := mapIssueStatus(status)
	if mapped == string(types.StatusOpen) {
		return string(types.StatusReady)
	}
	return mapped
}

func isTerminalTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case string(types.StatusCompleted), string(types.StatusDone):
		return true
	default:
		return false
	}
}

func isIssueTerminalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed", "done", "completed":
		return true
	default:
		return false
	}
}

func isBlockedIssueStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "blocked")
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
