package dag

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const globalPauseStateKey = "global_pause"

// SetGlobalPaused persists the global dispatch pause state.
func (d *DAG) SetGlobalPaused(ctx context.Context, paused bool) error {
	value := "0"
	if paused {
		value = "1"
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO system_state (key, value, updated_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = datetime('now')`,
		globalPauseStateKey, value,
	)
	if err != nil {
		return fmt.Errorf("set global pause state: %w", err)
	}
	return nil
}

// IsGlobalPaused reports whether system-wide dispatch is paused.
// Returns false when no DB row exists (use IsGlobalPauseSet to
// distinguish "unset" from "explicitly unpaused").
func (d *DAG) IsGlobalPaused(ctx context.Context) (bool, error) {
	paused, _, err := d.IsGlobalPauseSet(ctx)
	return paused, err
}

// IsGlobalPauseSet returns the DB pause state and whether the key
// exists. When isSet is false, callers should fall back to config.
func (d *DAG) IsGlobalPauseSet(ctx context.Context) (paused bool, isSet bool, err error) {
	var raw string
	err = d.db.QueryRowContext(ctx,
		"SELECT value FROM system_state WHERE key = ?",
		globalPauseStateKey,
	).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, false, nil
		}
		return false, false, fmt.Errorf("read global pause state: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true, nil
	default:
		return false, true, nil
	}
}
