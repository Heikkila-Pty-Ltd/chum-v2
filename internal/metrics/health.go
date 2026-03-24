package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// HealthReport contains feedback loop health metrics.
type HealthReport struct {
	BurnRate              float64            // total cost_usd in last 24h
	AttemptDistribution   map[int]int        // attempt_count → task count
	QuarantineCount       int                // active quarantine safety blocks
	LessonCount           int                // total lessons stored
	CostPerSuccessfulTask float64            // avg cost per completed task
	FailureCategories     map[string]int     // sub_reason → count
	TaskStatusCounts      map[string]int     // status → count
}

// CollectHealth queries both the DAG and traces databases to produce a health report.
func CollectHealth(ctx context.Context, dagDB, tracesDB *sql.DB) (*HealthReport, error) {
	r := &HealthReport{
		AttemptDistribution: make(map[int]int),
		FailureCategories:   make(map[string]int),
		TaskStatusCounts:    make(map[string]int),
	}

	// --- DAG database queries ---

	// Task status distribution
	rows, err := dagDB.QueryContext(ctx,
		"SELECT status, COUNT(*) FROM tasks GROUP BY status ORDER BY COUNT(*) DESC")
	if err != nil {
		return nil, fmt.Errorf("task status counts: %w", err)
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return nil, err
		}
		r.TaskStatusCounts[status] = count
	}
	rows.Close()

	// Attempt distribution
	rows, err = dagDB.QueryContext(ctx,
		"SELECT attempt_count, COUNT(*) FROM tasks GROUP BY attempt_count ORDER BY attempt_count")
	if err != nil {
		// attempt_count column might not exist yet on old DBs
		if !strings.Contains(err.Error(), "no such column") {
			return nil, fmt.Errorf("attempt distribution: %w", err)
		}
	} else {
		for rows.Next() {
			var attempts, count int
			if err := rows.Scan(&attempts, &count); err != nil {
				rows.Close()
				return nil, err
			}
			r.AttemptDistribution[attempts] = count
		}
		rows.Close()
	}

	// --- Traces database queries ---

	// Burn rate: total cost in last 24h
	err = tracesDB.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(cost_usd), 0) FROM perf_runs WHERE created_at > datetime('now', '-1 day')").
		Scan(&r.BurnRate)
	if err != nil {
		return nil, fmt.Errorf("burn rate: %w", err)
	}

	// Quarantine count from safety_blocks
	err = tracesDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM safety_blocks WHERE block_type = 'quarantine' AND blocked_until > datetime('now')").
		Scan(&r.QuarantineCount)
	if err != nil {
		// safety_blocks might not exist
		if !strings.Contains(err.Error(), "no such table") {
			return nil, fmt.Errorf("quarantine count: %w", err)
		}
	}

	// Lesson count
	err = tracesDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM lessons").
		Scan(&r.LessonCount)
	if err != nil {
		if !strings.Contains(err.Error(), "no such table") {
			return nil, fmt.Errorf("lesson count: %w", err)
		}
	}

	// Cost per successful task
	err = tracesDB.QueryRowContext(ctx,
		`SELECT COALESCE(AVG(cost_usd), 0)
		 FROM (SELECT task_id, SUM(cost_usd) as cost_usd
		       FROM perf_runs WHERE task_id != '' AND success = 1
		       GROUP BY task_id)`).
		Scan(&r.CostPerSuccessfulTask)
	if err != nil {
		// task_id column might not exist yet
		if !strings.Contains(err.Error(), "no such column") {
			return nil, fmt.Errorf("cost per task: %w", err)
		}
	}

	// Failure categories from execution_traces
	rows, err = tracesDB.QueryContext(ctx,
		`SELECT outcome, COUNT(*) FROM execution_traces
		 WHERE status != 'completed' AND status != 'decomposed' AND outcome != ''
		 GROUP BY outcome ORDER BY COUNT(*) DESC LIMIT 20`)
	if err != nil {
		if !strings.Contains(err.Error(), "no such table") {
			return nil, fmt.Errorf("failure categories: %w", err)
		}
	} else {
		for rows.Next() {
			var category string
			var count int
			if err := rows.Scan(&category, &count); err != nil {
				rows.Close()
				return nil, err
			}
			r.FailureCategories[category] = count
		}
		rows.Close()
	}

	return r, nil
}

// FormatReport produces a human-readable text report from a HealthReport.
func FormatReport(r *HealthReport) string {
	var b strings.Builder

	fmt.Fprintf(&b, "=== CHUM Feedback Loop Health ===\n\n")

	fmt.Fprintf(&b, "Burn Rate (24h):           $%.2f\n", r.BurnRate)
	fmt.Fprintf(&b, "Cost/Successful Task:      $%.2f\n", r.CostPerSuccessfulTask)
	fmt.Fprintf(&b, "Active Quarantines:        %d\n", r.QuarantineCount)
	fmt.Fprintf(&b, "Lessons Stored:            %d\n", r.LessonCount)

	if len(r.TaskStatusCounts) > 0 {
		fmt.Fprintf(&b, "\nTask Status Distribution:\n")
		for status, count := range r.TaskStatusCounts {
			fmt.Fprintf(&b, "  %-20s %d\n", status, count)
		}
	}

	if len(r.AttemptDistribution) > 0 {
		fmt.Fprintf(&b, "\nAttempt Distribution:\n")
		for attempts, count := range r.AttemptDistribution {
			fmt.Fprintf(&b, "  %d attempts:  %d tasks\n", attempts, count)
		}
	}

	if len(r.FailureCategories) > 0 {
		fmt.Fprintf(&b, "\nTop Failure Categories:\n")
		for cat, count := range r.FailureCategories {
			fmt.Fprintf(&b, "  %-30s %d\n", cat, count)
		}
	}

	return b.String()
}
