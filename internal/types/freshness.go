package types

import "time"

// FreshnessStatus represents the SLA state of a data source.
type FreshnessStatus string

const (
	FreshnessStatusOK       FreshnessStatus = "OK"
	FreshnessStatusWarn     FreshnessStatus = "WARN"
	FreshnessStatusCritical FreshnessStatus = "CRITICAL"
)

// SourceFreshness holds calculated freshness metrics for a data source.
type SourceFreshness struct {
	Status      FreshnessStatus `json:"status"`
	LastUpdated time.Time       `json:"last_updated"`
	NextRunETA  time.Time       `json:"next_run_eta"`
}
