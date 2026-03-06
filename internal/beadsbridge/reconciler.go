package beadsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// DriftClass is a deterministic drift category.
type DriftClass string

const (
	DriftMissingMapping     DriftClass = "missing_mapping"
	DriftStatusMismatch     DriftClass = "status_mismatch"
	DriftDependencyMismatch DriftClass = "dependency_mismatch"
	DriftOrphanedTask       DriftClass = "orphaned_chum_task"
)

// DriftItem is one reconciler finding.
type DriftItem struct {
	Class          DriftClass `json:"class"`
	IssueID        string     `json:"issue_id,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	Details        string     `json:"details,omitempty"`
	ProposedAction string     `json:"proposed_action"`
}

// ReconcileReport is one deterministic reconciliation snapshot.
type ReconcileReport struct {
	Project   string      `json:"project"`
	DryRun    bool        `json:"dry_run"`
	Generated time.Time   `json:"generated_at"`
	Items     []DriftItem `json:"items"`
}

// ReconcileProject compares beads + CHUM + map state and optionally applies allowlisted fixes.
func ReconcileProject(ctx context.Context, d *dag.DAG, client beads.Store, project string, apply bool, allow map[DriftClass]bool) (ReconcileReport, error) {
	if d == nil {
		return ReconcileReport{}, fmt.Errorf("nil DAG")
	}
	if client == nil {
		return ReconcileReport{}, fmt.Errorf("nil beads client")
	}
	issues, err := client.List(ctx, 0)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("list beads issues: %w", err)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	issueByID := make(map[string]beads.Issue, len(issues))
	for _, issue := range issues {
		issueByID[issue.ID] = issue
	}

	tasks, err := d.ListTasks(ctx, project)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("list DAG tasks: %w", err)
	}
	taskByID := make(map[string]dag.Task, len(tasks))
	for _, t := range tasks {
		taskByID[t.ID] = t
	}

	mappings, err := d.ListBeadsMappings(ctx, project)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("list mappings: %w", err)
	}
	mapByIssue := make(map[string]dag.BeadsSyncMapRow, len(mappings))
	mapByTask := make(map[string]dag.BeadsSyncMapRow, len(mappings))
	for _, m := range mappings {
		mapByIssue[m.IssueID] = m
		mapByTask[m.TaskID] = m
	}

	var items []DriftItem

	// Class: missing_mapping
	for _, issue := range issues {
		if _, ok := mapByIssue[issue.ID]; ok {
			continue
		}
		items = append(items, DriftItem{
			Class:          DriftMissingMapping,
			IssueID:        issue.ID,
			ProposedAction: "admit_or_map_issue",
			Details:        "issue exists in beads but has no mapping row",
		})
	}

	// Classes: status_mismatch + dependency_mismatch
	for _, issue := range issues {
		m, ok := mapByIssue[issue.ID]
		if !ok {
			continue
		}
		task, exists := taskByID[m.TaskID]
		if !exists {
			items = append(items, DriftItem{
				Class:          DriftMissingMapping,
				IssueID:        issue.ID,
				TaskID:         m.TaskID,
				ProposedAction: "recreate_task_or_remap",
				Details:        "mapping points to missing task",
			})
			continue
		}

		desired := mapIssueStatus(issue.Status)
		taskStatus := strings.TrimSpace(task.Status)
		// Never overwrite CHUM-internal statuses that beads doesn't understand.
		// These represent workflow states (decomposed, failed, needs_review, etc.)
		// that should only be changed by CHUM's engine, not by beads sync.
		chumInternal := isCHUMInternalStatus(taskStatus)
		if desired != "" && taskStatus != desired && !chumInternal {
			items = append(items, DriftItem{
				Class:          DriftStatusMismatch,
				IssueID:        issue.ID,
				TaskID:         task.ID,
				ProposedAction: "align_task_status",
				Details:        fmt.Sprintf("beads=%s dag=%s", issue.Status, task.Status),
			})
			if apply && allow[DriftStatusMismatch] {
				_ = d.UpdateTaskStatus(ctx, task.ID, desired)
			}
		}

		taskDeps, depErr := d.GetDependencies(ctx, task.ID)
		if depErr != nil {
			return ReconcileReport{}, fmt.Errorf("get dependencies for %s: %w", task.ID, depErr)
		}
		depSet := make(map[string]bool, len(taskDeps))
		for _, dep := range taskDeps {
			depSet[dep] = true
		}
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == "" {
				continue
			}
			depMap, ok := mapByIssue[dep.DependsOnID]
			if !ok {
				continue // unresolved upstream mapping; handled by missing_mapping class
			}
			if depSet[depMap.TaskID] {
				continue
			}
			items = append(items, DriftItem{
				Class:          DriftDependencyMismatch,
				IssueID:        issue.ID,
				TaskID:         task.ID,
				ProposedAction: "add_missing_dependency_edge",
				Details:        fmt.Sprintf("expected dep issue=%s task=%s", dep.DependsOnID, depMap.TaskID),
			})
			if apply && allow[DriftDependencyMismatch] {
				_ = d.AddEdgeWithSource(ctx, task.ID, depMap.TaskID, "beads_bridge")
			}
		}
	}

	// Class: orphaned_chum_task (bridge-managed task with no mapping row).
	for _, task := range tasks {
		if _, ok := mapByTask[task.ID]; ok {
			continue
		}
		if task.Metadata == nil || task.Metadata["beads_bridge"] != "true" {
			continue
		}
		items = append(items, DriftItem{
			Class:          DriftOrphanedTask,
			TaskID:         task.ID,
			ProposedAction: "archive_or_remap_task",
			Details:        "bridge-managed task has no mapping",
		})
	}

	sort.Slice(items, func(i, j int) bool {
		ai := fmt.Sprintf("%s|%s|%s|%s", items[i].Class, items[i].IssueID, items[i].TaskID, items[i].Details)
		aj := fmt.Sprintf("%s|%s|%s|%s", items[j].Class, items[j].IssueID, items[j].TaskID, items[j].Details)
		return ai < aj
	})

	report := ReconcileReport{
		Project:   project,
		DryRun:    !apply,
		Generated: time.Now().UTC(),
		Items:     items,
	}
	for _, item := range items {
		details, _ := json.Marshal(item)
		_ = d.InsertBeadsAudit(ctx, dag.BeadsSyncAuditRow{
			Project:   project,
			IssueID:   item.IssueID,
			TaskID:    item.TaskID,
			EventKind: "reconcile",
			Decision:  "drift_detected",
			Reason:    string(item.Class),
			Details:   string(details),
		})
	}
	summary, _ := json.Marshal(map[string]any{
		"dry_run": apply == false,
		"count":   len(items),
	})
	_ = d.InsertBeadsAudit(ctx, dag.BeadsSyncAuditRow{
		Project:   project,
		EventKind: "reconcile",
		Decision:  "summary",
		Reason:    "reconcile_complete",
		Details:   string(summary),
	})
	return report, nil
}

func normalizeTaskToBeadsStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case string(types.StatusCompleted), "done", "closed":
		return "done"
	case string(types.StatusRunning), "in_progress":
		return "in_progress"
	case string(types.StatusReady):
		return "ready"
	default:
		return "open"
	}
}
