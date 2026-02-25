// Package dag implements a SQLite-backed task graph for CHUM.
package dag

import "time"

// Task represents a unit of work in the DAG.
type Task struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Status          string    `json:"status"`
	Priority        int       `json:"priority"`
	Type            string    `json:"type"`
	Assignee        string    `json:"assignee"`
	Labels          []string  `json:"labels"`
	EstimateMinutes int       `json:"estimate_minutes"`
	ParentID        string    `json:"parent_id"`
	Acceptance      string    `json:"acceptance"`
	Project         string    `json:"project"`
	ErrorLog        string    `json:"error_log"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
