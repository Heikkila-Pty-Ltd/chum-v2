package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// GetBlock returns a safety block by scope and type, or nil if not found.
func (s *Store) GetBlock(scope, blockType string) (*SafetyBlock, error) {
	scope = strings.TrimSpace(scope)
	blockType = strings.TrimSpace(blockType)
	if scope == "" || blockType == "" {
		return nil, nil
	}

	var (
		b            SafetyBlock
		metadataJSON sql.NullString
	)
	err := s.db.QueryRow(
		`SELECT scope, block_type, blocked_until, reason, metadata, created_at, updated_at
		 FROM safety_blocks WHERE scope = ? AND block_type = ?`,
		scope, blockType,
	).Scan(&b.Scope, &b.BlockType, &b.BlockedUntil, &b.Reason, &metadataJSON, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get block: %w", err)
	}

	b.Metadata = make(map[string]interface{})
	if metadataJSON.Valid && strings.TrimSpace(metadataJSON.String) != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &b.Metadata); err != nil {
			return nil, fmt.Errorf("store: decode block metadata: %w", err)
		}
	}
	return &b, nil
}

// SetBlock creates or updates a safety block without metadata.
func (s *Store) SetBlock(scope, blockType string, blockedUntil time.Time, reason string) error {
	return s.SetBlockWithMetadata(scope, blockType, blockedUntil, reason, nil)
}

// SetBlockWithMetadata creates or updates a safety block with metadata.
func (s *Store) SetBlockWithMetadata(scope, blockType string, blockedUntil time.Time, reason string, metadata map[string]interface{}) error {
	scope = strings.TrimSpace(scope)
	blockType = strings.TrimSpace(blockType)
	reason = strings.TrimSpace(reason)
	if scope == "" || blockType == "" {
		return fmt.Errorf("store: set block: scope and block_type are required")
	}
	if blockedUntil.IsZero() {
		blockedUntil = time.Now()
	}
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("store: encode block metadata: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO safety_blocks (scope, block_type, blocked_until, reason, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(scope, block_type) DO UPDATE SET
		   blocked_until = excluded.blocked_until,
		   reason = excluded.reason,
		   metadata = excluded.metadata,
		   updated_at = datetime('now')`,
		scope, blockType, blockedUntil.UTC().Format(time.DateTime), reason, string(metadataJSON),
	)
	if err != nil {
		return fmt.Errorf("store: set block: %w", err)
	}
	return nil
}

// RemoveBlock deletes a safety block.
func (s *Store) RemoveBlock(scope, blockType string) error {
	scope = strings.TrimSpace(scope)
	blockType = strings.TrimSpace(blockType)
	if scope == "" || blockType == "" {
		return nil
	}
	_, err := s.db.Exec(
		`DELETE FROM safety_blocks WHERE scope = ? AND block_type = ?`,
		scope, blockType)
	if err != nil {
		return fmt.Errorf("store: remove block: %w", err)
	}
	return nil
}

// GetActiveBlocks returns all blocks whose blocked_until is in the future.
func (s *Store) GetActiveBlocks() ([]SafetyBlock, error) {
	rows, err := s.db.Query(
		`SELECT scope, block_type, blocked_until, reason, metadata, created_at, updated_at
		 FROM safety_blocks WHERE blocked_until > datetime('now')
		 ORDER BY block_type, scope`)
	if err != nil {
		return nil, fmt.Errorf("store: get active blocks: %w", err)
	}
	defer rows.Close()
	return scanBlocks(rows)
}

// GetBlockCountsByType returns counts of active blocks grouped by block_type.
func (s *Store) GetBlockCountsByType() (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT block_type, COUNT(*) FROM safety_blocks
		 WHERE blocked_until > datetime('now')
		 GROUP BY block_type ORDER BY block_type`)
	if err != nil {
		return nil, fmt.Errorf("store: get block counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var blockType string
		var count int
		if err := rows.Scan(&blockType, &count); err != nil {
			return nil, fmt.Errorf("store: scan block count: %w", err)
		}
		counts[blockType] = count
	}
	return counts, rows.Err()
}

// IsMorselValidating returns whether a morsel is currently marked as validating.
func (s *Store) IsMorselValidating(morselID string) (bool, error) {
	block, err := s.GetBlock(strings.TrimSpace(morselID), "morsel_validating")
	if err != nil {
		return false, err
	}
	if block == nil {
		return false, nil
	}
	return time.Now().Before(block.BlockedUntil), nil
}

// SetMorselValidating sets a validating block until the given time.
func (s *Store) SetMorselValidating(morselID string, until time.Time) error {
	return s.SetBlock(strings.TrimSpace(morselID), "morsel_validating", until, "morsel validating")
}

// ClearMorselValidating removes the validating block for a morsel.
func (s *Store) ClearMorselValidating(morselID string) error {
	return s.RemoveBlock(strings.TrimSpace(morselID), "morsel_validating")
}

// scanBlocks scans rows into SafetyBlock slices.
func scanBlocks(rows *sql.Rows) ([]SafetyBlock, error) {
	var blocks []SafetyBlock
	for rows.Next() {
		var b SafetyBlock
		var metadataJSON sql.NullString
		if err := rows.Scan(&b.Scope, &b.BlockType, &b.BlockedUntil, &b.Reason, &metadataJSON, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan block: %w", err)
		}
		b.Metadata = make(map[string]interface{})
		if metadataJSON.Valid && strings.TrimSpace(metadataJSON.String) != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &b.Metadata); err != nil {
				return nil, fmt.Errorf("store: decode block metadata: %w", err)
			}
		}
		blocks = append(blocks, b)
	}
	return blocks, rows.Err()
}
