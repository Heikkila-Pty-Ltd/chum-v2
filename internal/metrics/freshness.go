package metrics

import (
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// CalculateSourceFreshness calculates the freshness metrics for a given source.
// It determines the SLA status (OK, WARN, CRITICAL), the last updated timestamp,
// and the next run ETA based on the provided last run time and schedule cadence.
func CalculateSourceFreshness(sourceID string, now time.Time, lastRun time.Time, scheduleCadence time.Duration) types.SourceFreshness {
	nextRunETA := lastRun.Add(scheduleCadence)
	
	status := types.FreshnessStatusOK
	if now.After(nextRunETA) {
		// If current time is after the next expected run, it's at least a WARN.
		// Check how far behind it is.
		warnThreshold := lastRun.Add(2 * scheduleCadence)
		if now.After(warnThreshold) {
			status = types.FreshnessStatusCritical
		} else {
			status = types.FreshnessStatusWarn
		}
	}

	return types.SourceFreshness{
		Status:      status,
		LastUpdated: lastRun,
		NextRunETA:  nextRunETA,
	}
}
