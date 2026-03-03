package store

// schemaLessons contains the lessons table and FTS5 virtual table for
// full-text search over extracted lessons.
const schemaLessons = `
CREATE TABLE IF NOT EXISTS lessons (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL,
	project TEXT NOT NULL,
	category TEXT NOT NULL,
	summary TEXT NOT NULL,
	detail TEXT NOT NULL,
	file_paths TEXT NOT NULL DEFAULT '[]',
	labels TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_lessons_task ON lessons(task_id);
CREATE INDEX IF NOT EXISTS idx_lessons_project ON lessons(project);
CREATE INDEX IF NOT EXISTS idx_lessons_category ON lessons(category);
CREATE INDEX IF NOT EXISTS idx_lessons_created ON lessons(created_at);

CREATE VIRTUAL TABLE IF NOT EXISTS lessons_fts USING fts5(
	summary, detail, file_paths, labels,
	content='lessons',
	content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS lessons_ai AFTER INSERT ON lessons BEGIN
	INSERT INTO lessons_fts(rowid, summary, detail, file_paths, labels)
	VALUES (new.id, new.summary, new.detail, new.file_paths, new.labels);
END;

CREATE TRIGGER IF NOT EXISTS lessons_ad AFTER DELETE ON lessons BEGIN
	INSERT INTO lessons_fts(lessons_fts, rowid, summary, detail, file_paths, labels)
	VALUES ('delete', old.id, old.summary, old.detail, old.file_paths, old.labels);
END;

CREATE TRIGGER IF NOT EXISTS lessons_au AFTER UPDATE ON lessons BEGIN
	INSERT INTO lessons_fts(lessons_fts, rowid, summary, detail, file_paths, labels)
	VALUES ('delete', old.id, old.summary, old.detail, old.file_paths, old.labels);
	INSERT INTO lessons_fts(rowid, summary, detail, file_paths, labels)
	VALUES (new.id, new.summary, new.detail, new.file_paths, new.labels);
END;
`
