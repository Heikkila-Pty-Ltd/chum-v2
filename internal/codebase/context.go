// Package codebase provides shared codebase context gathering for planning
// and task execution. It combines AST parsing, vector search, keyword
// filtering, lessons FTS5, and DAG state into a single context result.
package codebase

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
)

// ContextResult holds gathered codebase context from multiple sources.
type ContextResult struct {
	// RelevantFiles are files semantically related to the query (full source).
	RelevantFiles []*ast.ParsedFile `json:"relevant_files,omitempty"`
	// SurroundingFiles are nearby files for broader awareness (signatures only).
	SurroundingFiles []*ast.ParsedFile `json:"surrounding_files,omitempty"`
	// AllFiles is the full parsed file set (used when no query-based filtering).
	AllFiles []*ast.ParsedFile `json:"all_files,omitempty"`
	// Lessons are FTS5-matched past solutions/learnings.
	Lessons []store.StoredLesson `json:"lessons,omitempty"`
	// ActiveTasks are currently active DAG tasks for deduplication awareness.
	ActiveTasks []dag.Task `json:"active_tasks,omitempty"`
	// ClaudeMD is the content of CLAUDE.md from the project workspace.
	ClaudeMD string `json:"claude_md,omitempty"`
}

// BuildOpts configures what context sources to gather.
type BuildOpts struct {
	Parser *ast.Parser      // AST parser; nil skips AST context
	Store  *store.Store     // lesson store; nil skips lessons
	DAG    *dag.DAG         // DAG store; nil skips active tasks
	Logger *slog.Logger     // logger for warnings; nil uses slog.Default()

	WorkDir string // project workspace path
	Project string // project name (for DAG queries)
	Query   string // semantic query (plan brief + user messages)
}

// activeStatuses are DAG task statuses that represent in-progress work.
var activeStatuses = []string{
	"open", "ready", "running",
	"needs_review", "needs_refinement",
	"dod_failed", "failed",
}

// Build gathers codebase context from all available sources.
// Each source is best-effort — failures are logged but never block.
func Build(ctx context.Context, opts BuildOpts) *ContextResult {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	result := &ContextResult{}

	// 1. AST + embeddings/keyword filtering
	if opts.Parser != nil && opts.WorkDir != "" {
		files, err := opts.Parser.ParseDir(ctx, opts.WorkDir)
		if err != nil {
			logger.Warn("codebase context: AST parse failed", "error", err)
		} else if len(files) > 0 {
			if opts.Query != "" {
				ef := ast.NewEmbedFilter()
				relevant, surrounding := ef.FilterRelevantByEmbedding(ctx, opts.Query, files)
				if len(relevant) > 0 {
					result.RelevantFiles = relevant
					result.SurroundingFiles = surrounding
					logger.Info("codebase context: embedding filter",
						"relevant", len(relevant),
						"surrounding", len(surrounding),
						"total", len(files))
				} else {
					// Embedding unavailable or no matches — use keyword fallback
					relevant, surrounding = ast.FilterRelevant(opts.Query, files)
					if len(relevant) > 0 {
						result.RelevantFiles = relevant
						result.SurroundingFiles = surrounding
						logger.Info("codebase context: keyword filter",
							"relevant", len(relevant),
							"surrounding", len(surrounding))
					} else {
						result.AllFiles = files
					}
				}
			} else {
				result.AllFiles = files
			}
		}
	}

	// 2. Lessons FTS5
	if opts.Store != nil && opts.Query != "" {
		lessons, err := opts.Store.SearchLessons(opts.Query, 5)
		if err != nil {
			logger.Warn("codebase context: lessons search failed", "error", err)
		} else {
			result.Lessons = lessons
		}
	}

	// 3. DAG active tasks
	if opts.DAG != nil && opts.Project != "" {
		tasks, err := opts.DAG.ListTasks(ctx, opts.Project, activeStatuses...)
		if err != nil {
			logger.Warn("codebase context: DAG query failed", "error", err)
		} else {
			result.ActiveTasks = tasks
		}
	}

	// 4. CLAUDE.md
	if opts.WorkDir != "" {
		data, err := os.ReadFile(filepath.Join(opts.WorkDir, "CLAUDE.md"))
		if err == nil {
			result.ClaudeMD = string(data)
		}
		// Not finding CLAUDE.md is fine — don't log.
	}

	return result
}

// HasContent returns true if the context result has any useful content.
func (r *ContextResult) HasContent() bool {
	return len(r.RelevantFiles) > 0 ||
		len(r.SurroundingFiles) > 0 ||
		len(r.AllFiles) > 0 ||
		len(r.Lessons) > 0 ||
		len(r.ActiveTasks) > 0 ||
		r.ClaudeMD != ""
}

// FormatAST returns the AST context as a string, matching the existing
// buildCodebaseContextForTask behavior. Relevant files get full source,
// surrounding files get signatures only.
func (r *ContextResult) FormatAST() string {
	if len(r.RelevantFiles) > 0 {
		return ast.SummarizeTargeted(r.SurroundingFiles, r.RelevantFiles)
	}
	if len(r.AllFiles) > 0 {
		// Cap to first 20 files to prevent prompt overflow.
		files := r.AllFiles
		if len(files) > 20 {
			files = files[:20]
		}
		return ast.Summarize(files)
	}
	return ""
}

