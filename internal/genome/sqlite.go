package genome

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS genome_species (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	agent TEXT NOT NULL,
	model TEXT NOT NULL,
	tier TEXT NOT NULL DEFAULT 'balanced',
	prompt_style TEXT NOT NULL DEFAULT 'minimal',
	tool_set TEXT NOT NULL DEFAULT '[]',
	parent_id TEXT,
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS genome_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	species_id TEXT NOT NULL REFERENCES genome_species(id),
	success INTEGER NOT NULL,
	reward REAL NOT NULL DEFAULT 0,
	duration_s REAL NOT NULL DEFAULT 0,
	tokens INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS genome_tool_patterns (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	species_id TEXT NOT NULL REFERENCES genome_species(id),
	sequence TEXT NOT NULL,
	reward REAL NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_genome_runs_species ON genome_runs(species_id);
CREATE INDEX IF NOT EXISTS idx_genome_runs_created ON genome_runs(created_at);
CREATE INDEX IF NOT EXISTS idx_genome_patterns_species ON genome_tool_patterns(species_id);
`

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite-backed genome store.
// Automatically creates tables if they don't exist.
func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("genome: create schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Schema returns the SQL schema for external migration use.
func Schema() string { return schema }

func (s *SQLiteStore) CreateSpecies(ctx context.Context, sp Species) error {
	toolSetJSON, err := json.Marshal(sp.ToolSet)
	if err != nil {
		return fmt.Errorf("genome: marshal tool set: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO genome_species (id, name, agent, model, tier, prompt_style, tool_set, parent_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sp.ID, sp.Name, sp.Agent, sp.Model, sp.Tier, sp.PromptStyle,
		string(toolSetJSON), sp.ParentID, sp.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
	)
	return err
}

func (s *SQLiteStore) GetSpecies(ctx context.Context, id string) (*Species, error) {
	var sp Species
	var toolSetJSON string
	var parentID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, agent, model, tier, prompt_style, tool_set, parent_id, created_at
		FROM genome_species WHERE id = ?`, id,
	).Scan(&sp.ID, &sp.Name, &sp.Agent, &sp.Model, &sp.Tier,
		&sp.PromptStyle, &toolSetJSON, &parentID, &sp.CreatedAt)
	if err != nil {
		return nil, err
	}
	if parentID.Valid {
		sp.ParentID = parentID.String
	}
	if err := json.Unmarshal([]byte(toolSetJSON), &sp.ToolSet); err != nil {
		sp.ToolSet = nil
	}
	return &sp, nil
}

func (s *SQLiteStore) ListSpecies(ctx context.Context) ([]Species, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, agent, model, tier, prompt_style, tool_set, parent_id, created_at
		FROM genome_species ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var species []Species
	for rows.Next() {
		var sp Species
		var toolSetJSON string
		var parentID sql.NullString
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.Agent, &sp.Model, &sp.Tier,
			&sp.PromptStyle, &toolSetJSON, &parentID, &sp.CreatedAt); err != nil {
			return nil, err
		}
		if parentID.Valid {
			sp.ParentID = parentID.String
		}
		if err := json.Unmarshal([]byte(toolSetJSON), &sp.ToolSet); err != nil {
			sp.ToolSet = nil
		}
		species = append(species, sp)
	}
	return species, rows.Err()
}

func (s *SQLiteStore) RecordRun(ctx context.Context, speciesID string, success bool, reward float64, durationS float64, tokens int) error {
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO genome_runs (species_id, success, reward, duration_s, tokens)
		VALUES (?, ?, ?, ?, ?)`,
		speciesID, successInt, reward, durationS, tokens,
	)
	return err
}

func (s *SQLiteStore) GetFitness(ctx context.Context, speciesID string) (*Fitness, error) {
	var f Fitness
	f.SpeciesID = speciesID
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total_runs,
			SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) as successes,
			SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END) as failures,
			CASE WHEN COUNT(*) > 0 THEN CAST(SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) AS REAL) / COUNT(*) ELSE 0 END as success_rate,
			COALESCE(AVG(reward), 0) as avg_reward,
			COALESCE(AVG(duration_s), 0) as avg_duration_s,
			COALESCE(AVG(tokens), 0) as avg_tokens
		FROM genome_runs WHERE species_id = ?`, speciesID,
	).Scan(&f.TotalRuns, &f.Successes, &f.Failures, &f.SuccessRate,
		&f.AvgReward, &f.AvgDurationS, &f.AvgTokens)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *SQLiteStore) GetAllFitness(ctx context.Context) ([]Fitness, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			gs.id,
			COUNT(gr.id) as total_runs,
			SUM(CASE WHEN gr.success = 1 THEN 1 ELSE 0 END) as successes,
			SUM(CASE WHEN gr.success = 0 THEN 1 ELSE 0 END) as failures,
			CASE WHEN COUNT(gr.id) > 0 THEN CAST(SUM(CASE WHEN gr.success = 1 THEN 1 ELSE 0 END) AS REAL) / COUNT(gr.id) ELSE 0 END as success_rate,
			COALESCE(AVG(gr.reward), 0) as avg_reward,
			COALESCE(AVG(gr.duration_s), 0) as avg_duration_s,
			COALESCE(CAST(AVG(gr.tokens) AS INTEGER), 0) as avg_tokens
		FROM genome_species gs
		LEFT JOIN genome_runs gr ON gs.id = gr.species_id
		GROUP BY gs.id
		ORDER BY success_rate DESC, total_runs DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []Fitness
	for rows.Next() {
		var f Fitness
		if err := rows.Scan(&f.SpeciesID, &f.TotalRuns, &f.Successes, &f.Failures,
			&f.SuccessRate, &f.AvgReward, &f.AvgDurationS, &f.AvgTokens); err != nil {
			return nil, err
		}
		all = append(all, f)
	}
	return all, rows.Err()
}

func (s *SQLiteStore) RecordToolPattern(ctx context.Context, speciesID string, sequence []string, reward float64) error {
	seqJSON, err := json.Marshal(sequence)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO genome_tool_patterns (species_id, sequence, reward)
		VALUES (?, ?, ?)`, speciesID, string(seqJSON), reward)
	return err
}

func (s *SQLiteStore) GetTopToolPatterns(ctx context.Context, speciesID string, limit int) ([]ToolPattern, error) {
	// Aggregate patterns by sequence, counting occurrences and averaging reward
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, COUNT(*) as cnt, AVG(reward) as avg_reward
		FROM genome_tool_patterns
		WHERE species_id = ?
		GROUP BY sequence
		HAVING cnt >= 2
		ORDER BY avg_reward DESC, cnt DESC
		LIMIT ?`, speciesID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var patterns []ToolPattern
	for rows.Next() {
		var p ToolPattern
		var seqJSON string
		if err := rows.Scan(&seqJSON, &p.Count, &p.AvgReward); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(seqJSON), &p.Sequence); err != nil {
			continue
		}
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

// PruneOldRuns removes run data older than the given duration.
// Keeps the genome responsive to recent performance rather than ancient history.
func (s *SQLiteStore) PruneOldRuns(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UTC().Format("2006-01-02 15:04:05")
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM genome_runs WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// FormatLeaderboard returns a human-readable species leaderboard.
func FormatLeaderboard(recs []Recommendation) string {
	if len(recs) == 0 {
		return "No species data yet."
	}
	var sb strings.Builder
	for i, r := range recs {
		sb.WriteString(fmt.Sprintf("%d. %s — %.0f%% success (%d runs), avg reward %.2f, avg %.0fs, confidence %.0f%%\n",
			i+1, r.Species.Name, r.Fitness.SuccessRate*100, r.Fitness.TotalRuns,
			r.Fitness.AvgReward, r.Fitness.AvgDurationS, r.Confidence*100))
	}
	return sb.String()
}
