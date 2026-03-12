package codebase

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
)

const maxClaudeMDChars = 2000 // ~500 tokens
const maxDirTreeFiles = 80   // cap directory map to avoid prompt overflow

// FormatForPrompt formats a ContextResult into a compact string suitable for
// injection into an LLM prompt. Uses progressive disclosure:
//
//   - Layer 1: Project directory map (all files, one line each with symbol counts)
//   - Layer 2: For query-matched files, show exported type/func signatures
//   - CLAUDE.md, active tasks, and lessons are included as concise sections
//
// This gives the LLM a map of what exists without dumping source code.
func FormatForPrompt(r *ContextResult) string {
	if r == nil || !r.HasContent() {
		return ""
	}

	var sb strings.Builder

	// CLAUDE.md (project conventions) — most important for planning
	if r.ClaudeMD != "" {
		sb.WriteString("\n### Project Conventions (CLAUDE.md)\n")
		md := r.ClaudeMD
		if len(md) > maxClaudeMDChars {
			md = md[:maxClaudeMDChars] + "\n... (truncated)"
		}
		sb.WriteString(md)
		sb.WriteByte('\n')
	}

	// Codebase map — progressive disclosure
	allFiles := collectAllFiles(r)
	if len(allFiles) > maxDirTreeFiles {
		allFiles = allFiles[:maxDirTreeFiles]
	}
	if len(allFiles) > 0 {
		// Layer 1: Directory map (compact, one line per file)
		sb.WriteString("\n### Codebase Map\n")
		sb.WriteString("Files with their packages and exported symbol counts.\n")
		sb.WriteString("Files marked with * are most relevant to this plan.\n\n")

		relevantPaths := relevantPathSet(r)
		tree := buildDirTree(allFiles, relevantPaths)
		sb.WriteString(tree)
		sb.WriteByte('\n')

		// Layer 2: Signatures for relevant files only
		relevant := r.RelevantFiles
		if len(relevant) == 0 && len(r.SurroundingFiles) > 0 {
			relevant = r.SurroundingFiles
		}
		if len(relevant) > 0 {
			sb.WriteString("\n### Key File Signatures\n")
			sb.WriteString("Exported types and functions in the most relevant files.\n\n")
			for _, f := range relevant {
				formatFileSignatures(&sb, f)
			}
		}
	}

	// Active DAG tasks (compact)
	if len(r.ActiveTasks) > 0 {
		sb.WriteString("\n### Active Goals & Tasks\n")
		sb.WriteString("Avoid duplicating work that's already in progress.\n\n")
		formatActiveTasks(&sb, r)
	}

	// Lessons (compact)
	if len(r.Lessons) > 0 {
		sb.WriteString("\n### Past Learnings\n")
		for _, l := range r.Lessons {
			sb.WriteString(fmt.Sprintf("- **%s** [%s]: %s\n", l.Category, l.Project, l.Summary))
		}
	}

	return sb.String()
}

// collectAllFiles merges all file lists from the context result.
func collectAllFiles(r *ContextResult) []*ast.ParsedFile {
	seen := make(map[string]bool)
	var all []*ast.ParsedFile
	for _, list := range [][]*ast.ParsedFile{r.RelevantFiles, r.SurroundingFiles, r.AllFiles} {
		for _, f := range list {
			if !seen[f.Path] {
				seen[f.Path] = true
				all = append(all, f)
			}
		}
	}
	return all
}

// relevantPathSet builds a set of file paths that matched the query.
func relevantPathSet(r *ContextResult) map[string]bool {
	s := make(map[string]bool)
	for _, f := range r.RelevantFiles {
		s[f.Path] = true
	}
	return s
}

// buildDirTree creates a compact directory-organized file listing.
// Each file shows: path, package, exported symbol count, and a * if relevant.
func buildDirTree(files []*ast.ParsedFile, relevant map[string]bool) string {
	// Group by directory.
	type fileEntry struct {
		name     string
		pkg      string
		exported int
		star     bool
	}
	dirs := make(map[string][]fileEntry)
	for _, f := range files {
		dir := filepath.Dir(f.Path)
		name := filepath.Base(f.Path)
		exported := 0
		for _, sym := range f.Symbols {
			if len(sym.Name) > 0 && sym.Name[0] >= 'A' && sym.Name[0] <= 'Z' {
				exported++
			}
		}
		dirs[dir] = append(dirs[dir], fileEntry{
			name:     name,
			pkg:      f.Package,
			exported: exported,
			star:     relevant[f.Path],
		})
	}

	// Sort directories.
	dirNames := make([]string, 0, len(dirs))
	for d := range dirs {
		dirNames = append(dirNames, d)
	}
	sort.Strings(dirNames)

	var sb strings.Builder
	for _, dir := range dirNames {
		entries := dirs[dir]
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		sb.WriteString(fmt.Sprintf("%s/\n", dir))
		for _, e := range entries {
			marker := " "
			if e.star {
				marker = "*"
			}
			sb.WriteString(fmt.Sprintf(" %s %-40s pkg:%-15s exports:%d\n", marker, e.name, e.pkg, e.exported))
		}
	}
	return sb.String()
}

// formatFileSignatures writes exported type and function signatures for a file.
func formatFileSignatures(sb *strings.Builder, f *ast.ParsedFile) {
	var sigs []string
	for _, sym := range f.Symbols {
		if len(sym.Name) > 0 && sym.Name[0] >= 'A' && sym.Name[0] <= 'Z' {
			sigs = append(sigs, sym.Signature)
		}
	}
	if len(sigs) == 0 {
		return
	}
	sb.WriteString(fmt.Sprintf("// %s (package %s)\n", f.Path, f.Package))
	for _, sig := range sigs {
		sb.WriteString(sig)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
}

// formatActiveTasks writes a compact grouped view of active DAG tasks.
func formatActiveTasks(sb *strings.Builder, r *ContextResult) {
	// Group by goal (parent_id)
	goals := make(map[string][]string)
	goalTitles := make(map[string]string)
	var orphans []string
	for _, t := range r.ActiveTasks {
		if t.ParentID == "" {
			goalTitles[t.ID] = t.Title
		}
	}
	for _, t := range r.ActiveTasks {
		if t.ParentID != "" {
			goals[t.ParentID] = append(goals[t.ParentID],
				fmt.Sprintf("  - [%s] %s", t.Status, t.Title))
		} else if _, isGoal := goalTitles[t.ID]; !isGoal {
			orphans = append(orphans, fmt.Sprintf("- [%s] %s", t.Status, t.Title))
		}
	}
	for goalID, title := range goalTitles {
		sb.WriteString(fmt.Sprintf("- **%s** (%s)\n", title, goalID))
		for _, child := range goals[goalID] {
			sb.WriteString(child)
			sb.WriteByte('\n')
		}
	}
	for _, o := range orphans {
		sb.WriteString(o)
		sb.WriteByte('\n')
	}
}
