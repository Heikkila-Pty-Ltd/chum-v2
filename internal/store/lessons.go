package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StoreLesson persists a lesson and updates the FTS5 index via triggers.
func (s *Store) StoreLesson(morselID, project, category, summary, detail string, filePaths []string, labels []string) (int64, error) {
	filePathsJSON, err := json.Marshal(filePaths)
	if err != nil {
		return 0, fmt.Errorf("store: marshal file_paths: %w", err)
	}
	labelsStr := strings.Join(labels, ",")

	result, err := s.db.Exec(`
		INSERT INTO lessons (morsel_id, project, category, summary, detail, file_paths, labels)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		morselID, project, category, summary, detail, string(filePathsJSON), labelsStr,
	)
	if err != nil {
		return 0, fmt.Errorf("store: insert lesson: %w", err)
	}
	return result.LastInsertId()
}

// SearchLessons performs FTS5 full-text search, ordered by BM25 relevance.
func (s *Store) SearchLessons(query string, limit int) ([]StoredLesson, error) {
	if limit <= 0 {
		limit = 10
	}
	query = sanitizeFTS5Query(query)
	if query == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT l.id, l.morsel_id, l.project, l.category, l.summary, l.detail,
		       l.file_paths, l.labels, l.created_at
		FROM lessons l
		JOIN lessons_fts f ON l.id = f.rowid
		WHERE lessons_fts MATCH ?
		ORDER BY bm25(lessons_fts)
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search lessons: %w", err)
	}
	defer rows.Close()
	return scanLessons(rows)
}

// SearchLessonsByFilePath returns lessons whose file_paths overlap with given paths.
func (s *Store) SearchLessonsByFilePath(filePaths []string, limit int) ([]StoredLesson, error) {
	if len(filePaths) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	// Split paths into FTS tokens.
	seen := make(map[string]bool)
	var terms []string
	for _, p := range filePaths {
		for _, part := range strings.FieldsFunc(p, func(r rune) bool {
			return r == '/' || r == '.' || r == '_' || r == '-'
		}) {
			part = strings.TrimSpace(part)
			if part != "" && len(part) > 1 && !seen[part] {
				seen[part] = true
				terms = append(terms, part)
			}
		}
	}
	if len(terms) == 0 {
		return nil, nil
	}

	ftsQuery := strings.Join(terms, " OR ")
	return s.SearchLessons(ftsQuery, limit)
}

// GetRecentLessons returns the N most recent lessons for a project.
func (s *Store) GetRecentLessons(project string, limit int) ([]StoredLesson, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, morsel_id, project, category, summary, detail,
		       file_paths, labels, created_at
		FROM lessons WHERE project = ?
		ORDER BY created_at DESC LIMIT ?`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("store: get recent lessons: %w", err)
	}
	defer rows.Close()
	return scanLessons(rows)
}

// GetLessonsByMorsel returns all lessons for a specific morsel.
func (s *Store) GetLessonsByMorsel(morselID string) ([]StoredLesson, error) {
	rows, err := s.db.Query(`
		SELECT id, morsel_id, project, category, summary, detail,
		       file_paths, labels, created_at
		FROM lessons WHERE morsel_id = ?
		ORDER BY created_at DESC`, morselID)
	if err != nil {
		return nil, fmt.Errorf("store: get lessons by morsel: %w", err)
	}
	defer rows.Close()
	return scanLessons(rows)
}

// CountLessons returns the total number of lessons, optionally filtered by project.
func (s *Store) CountLessons(project string) (int, error) {
	var count int
	var err error
	if project == "" {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM lessons`).Scan(&count)
	} else {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM lessons WHERE project = ?`, project).Scan(&count)
	}
	if err != nil {
		return 0, fmt.Errorf("store: count lessons: %w", err)
	}
	return count, nil
}

// scanLessons scans rows into StoredLesson slices.
func scanLessons(rows *sql.Rows) ([]StoredLesson, error) {
	var lessons []StoredLesson
	for rows.Next() {
		var l StoredLesson
		var filePathsJSON, labelsStr, createdAt string
		if err := rows.Scan(&l.ID, &l.MorselID, &l.Project, &l.Category,
			&l.Summary, &l.Detail, &filePathsJSON, &labelsStr, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scan lesson: %w", err)
		}
		if filePathsJSON != "" && filePathsJSON != "[]" {
			_ = json.Unmarshal([]byte(filePathsJSON), &l.FilePaths)
		}
		if labelsStr != "" {
			l.Labels = strings.Split(labelsStr, ",")
		}
		if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			l.CreatedAt = t
		}
		lessons = append(lessons, l)
	}
	return lessons, rows.Err()
}

// fts5MetaChars are characters with special meaning in FTS5 MATCH expressions.
const fts5MetaChars = `()*{}^~:`

// sanitizeFTS5Query quotes each non-operator term to prevent misinterpretation.
// Only uppercase OR, AND, NOT are preserved as FTS5 boolean operators.
func sanitizeFTS5Query(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return query
	}
	var out []string
	for _, w := range words {
		if w == "OR" || w == "AND" || w == "NOT" {
			out = append(out, w)
			continue
		}
		w = strings.Trim(w, `"`)
		w = stripChars(w, fts5MetaChars)
		if w != "" {
			out = append(out, `"`+w+`"`)
		}
	}
	return strings.Join(out, " ")
}

// stripChars removes all characters in chars from s.
func stripChars(s, chars string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(chars, r) {
			return -1
		}
		return r
	}, s)
}
