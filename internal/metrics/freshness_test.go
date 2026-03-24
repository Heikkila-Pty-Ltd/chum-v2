package metrics

import (
	"testing"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

func TestCalculateSourceFreshness(t *testing.T) {
	now := time.Date(2026, time.March, 21, 12, 0, 0, 0, time.UTC)
	cadence := 1 * time.Hour

	tests := []struct {
		name           string
		lastRun        time.Time
		expectedStatus types.FreshnessStatus
		expectedETA    time.Time
	}{
		{
			name:           "OK - last run is fresh",
			lastRun:        now.Add(-30 * time.Minute), // 30 minutes ago
			expectedStatus: types.FreshnessStatusOK,
			expectedETA:    now.Add(30 * time.Minute), // (now - 30min) + 1h = now + 30min
		},
		{
			name:           "OK - last run at cadence boundary",
			lastRun:        now.Add(-1 * time.Hour), // 1 hour ago
			expectedStatus: types.FreshnessStatusOK,
			expectedETA:    now, // (now - 1h) + 1h = now
		},
		{
			name:           "WARN - slightly past cadence",
			lastRun:        now.Add(-1 * time.Hour).Add(-1 * time.Minute), // 1h 1min ago
			expectedStatus: types.FreshnessStatusWarn,
			expectedETA:    now.Add(-1 * time.Minute), // (now - 1h 1min) + 1h = now - 1min
		},
		{
			name:           "WARN - almost at critical threshold",
			lastRun:        now.Add(-2 * time.Hour).Add(1 * time.Minute), // 1h 59min ago
			expectedStatus: types.FreshnessStatusWarn,
			expectedETA:    now.Add(-59 * time.Minute), // (now - 1h 59min) + 1h = now - 59min
		},
		{
			name:           "CRITICAL - past twice the cadence",
			lastRun:        now.Add(-2 * time.Hour).Add(-1 * time.Minute), // 2h 1min ago
			expectedStatus: types.FreshnessStatusCritical,
			expectedETA:    now.Add(-1 * time.Hour).Add(-1 * time.Minute), // (now - 2h 1min) + 1h = now - 1h 1min
		},
		{
			name:           "CRITICAL - very old",
			lastRun:        now.Add(-5 * time.Hour), // 5 hours ago
			expectedStatus: types.FreshnessStatusCritical,
			expectedETA:    now.Add(-4 * time.Hour), // (now - 5h) + 1h = now - 4h
		},
		{
			name:           "OK - last run in future",
			lastRun:        now.Add(30 * time.Minute), // 30 minutes in future
			expectedStatus: types.FreshnessStatusOK,
			expectedETA:    now.Add(1 * time.Hour).Add(30 * time.Minute), // (now + 30min) + 1h = now + 1h 30min
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			freshness := CalculateSourceFreshness("test-source", now, tt.lastRun, cadence)

			if freshness.Status != tt.expectedStatus {
				t.Errorf("Expected status %s, got %s", tt.expectedStatus, freshness.Status)
			}
			if !freshness.LastUpdated.Equal(tt.lastRun) {
				t.Errorf("Expected LastUpdated %s, got %s", tt.lastRun, freshness.LastUpdated)
			}
			if freshness.SourceID != "test-source" {
				t.Errorf("Expected SourceID test-source, got %s", freshness.SourceID)
			}
			if !freshness.NextRunETA.Equal(tt.expectedETA) {
				t.Errorf("Expected NextRunETA %s, got %s", tt.expectedETA, freshness.NextRunETA)
			}
		})
	}
}
