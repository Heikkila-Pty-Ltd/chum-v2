package codebase

import (
	"fmt"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
)

func TestHasContent(t *testing.T) {
	tests := []struct {
		name string
		r    *ContextResult
		want bool
	}{
		{"nil", nil, false},
		{"empty", &ContextResult{}, false},
		{"with ClaudeMD", &ContextResult{ClaudeMD: "# Rules"}, true},
		{"with relevant files", &ContextResult{RelevantFiles: []*ast.ParsedFile{{Path: "a.go"}}}, true},
		{"with lessons", &ContextResult{Lessons: []store.StoredLesson{{Summary: "x"}}}, true},
		{"with active tasks", &ContextResult{ActiveTasks: []dag.Task{{ID: "1"}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.r != nil && tt.r.HasContent()
			if got != tt.want {
				t.Errorf("HasContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatForPrompt_Nil(t *testing.T) {
	if got := FormatForPrompt(nil); got != "" {
		t.Errorf("FormatForPrompt(nil) = %q, want empty", got)
	}
}

func TestFormatForPrompt_Empty(t *testing.T) {
	if got := FormatForPrompt(&ContextResult{}); got != "" {
		t.Errorf("FormatForPrompt(empty) = %q, want empty", got)
	}
}

func TestFormatForPrompt_ClaudeMD(t *testing.T) {
	r := &ContextResult{ClaudeMD: "# Project Rules\nUse Go."}
	out := FormatForPrompt(r)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "Project Conventions") {
		t.Error("missing Project Conventions header")
	}
	if !contains(out, "Use Go") {
		t.Error("missing ClaudeMD content")
	}
}

func TestFormatForPrompt_ClaudeMD_Truncation(t *testing.T) {
	long := make([]byte, maxClaudeMDChars+500)
	for i := range long {
		long[i] = 'x'
	}
	r := &ContextResult{ClaudeMD: string(long)}
	out := FormatForPrompt(r)
	if !contains(out, "truncated") {
		t.Error("long ClaudeMD should be truncated")
	}
}

func TestFormatForPrompt_DirectoryMap(t *testing.T) {
	r := &ContextResult{
		RelevantFiles: []*ast.ParsedFile{
			{
				Path:    "internal/foo/bar.go",
				Package: "foo",
				Symbols: []ast.Symbol{
					{Name: "FooService", Signature: "type FooService struct{}"},
					{Name: "helper", Signature: "func helper()"},
				},
			},
		},
		SurroundingFiles: []*ast.ParsedFile{
			{
				Path:    "internal/foo/baz.go",
				Package: "foo",
				Symbols: []ast.Symbol{
					{Name: "BazHelper", Signature: "func BazHelper()"},
				},
			},
		},
	}
	out := FormatForPrompt(r)

	// Directory map should show both files
	if !contains(out, "Codebase Map") {
		t.Error("missing Codebase Map header")
	}
	if !contains(out, "bar.go") {
		t.Error("missing relevant file in map")
	}
	if !contains(out, "baz.go") {
		t.Error("missing surrounding file in map")
	}

	// Relevant file should be starred
	if !contains(out, "*") {
		t.Error("relevant file should be starred")
	}

	// Layer 2: signatures for relevant file (FooService is exported)
	if !contains(out, "Key File Signatures") {
		t.Error("missing Key File Signatures header")
	}
	if !contains(out, "FooService") {
		t.Error("missing exported symbol signature")
	}

	// Unexported symbols should NOT appear in signatures
	if contains(out, "func helper()") {
		t.Error("unexported symbol should not appear in signatures")
	}
}

func TestFormatForPrompt_ActiveTasks(t *testing.T) {
	r := &ContextResult{
		ActiveTasks: []dag.Task{
			{ID: "goal-1", Title: "Build Auth", ParentID: "", Status: "open"},
			{ID: "task-1", Title: "Add JWT", ParentID: "goal-1", Status: "running"},
		},
	}
	out := FormatForPrompt(r)
	if !contains(out, "Active Goals") {
		t.Error("missing Active Goals header")
	}
	if !contains(out, "Build Auth") {
		t.Error("missing goal title")
	}
	if !contains(out, "Add JWT") {
		t.Error("missing child task")
	}
}

func TestFormatForPrompt_Lessons(t *testing.T) {
	r := &ContextResult{
		Lessons: []store.StoredLesson{
			{Category: "performance", Project: "chum", Summary: "Use batch queries"},
		},
	}
	out := FormatForPrompt(r)
	if !contains(out, "Past Learnings") {
		t.Error("missing Past Learnings header")
	}
	if !contains(out, "Use batch queries") {
		t.Error("missing lesson content")
	}
}

func TestBuildDirTree_Sorting(t *testing.T) {
	files := []*ast.ParsedFile{
		{Path: "b/z.go", Package: "b"},
		{Path: "a/y.go", Package: "a"},
		{Path: "a/x.go", Package: "a"},
	}
	tree := buildDirTree(files, nil)

	// Directories should be sorted: a/ before b/
	aIdx := indexOf(tree, "a/")
	bIdx := indexOf(tree, "b/")
	if aIdx < 0 || bIdx < 0 {
		t.Fatal("missing directory entries")
	}
	if aIdx > bIdx {
		t.Error("directories should be sorted alphabetically")
	}

	// Files within a directory should be sorted: x.go before y.go
	xIdx := indexOf(tree, "x.go")
	yIdx := indexOf(tree, "y.go")
	if xIdx > yIdx {
		t.Error("files within directory should be sorted")
	}
}

func TestCollectAllFiles_Deduplication(t *testing.T) {
	f := &ast.ParsedFile{Path: "a.go"}
	r := &ContextResult{
		RelevantFiles:    []*ast.ParsedFile{f},
		SurroundingFiles: []*ast.ParsedFile{f},
		AllFiles:         []*ast.ParsedFile{f},
	}
	all := collectAllFiles(r)
	if len(all) != 1 {
		t.Errorf("expected 1 deduplicated file, got %d", len(all))
	}
}

func TestFormatAST_CapsAllFiles(t *testing.T) {
	files := make([]*ast.ParsedFile, 25)
	for i := range files {
		files[i] = &ast.ParsedFile{Path: "file.go", Package: "main"}
	}
	r := &ContextResult{AllFiles: files}
	// FormatAST should cap to 20 files (not crash on 25)
	_ = r.FormatAST()
}

func TestFormatForPrompt_DirTreeCap(t *testing.T) {
	// Create more files than maxDirTreeFiles to verify capping
	files := make([]*ast.ParsedFile, 100)
	for i := range files {
		files[i] = &ast.ParsedFile{
			Path:    fmt.Sprintf("pkg/file_%03d.go", i),
			Package: "pkg",
		}
	}
	r := &ContextResult{AllFiles: files}
	out := FormatForPrompt(r)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	// Should not contain all 100 files — capped at maxDirTreeFiles
	count := 0
	for i := range files {
		name := fmt.Sprintf("file_%03d.go", i)
		if contains(out, name) {
			count++
		}
	}
	if count > maxDirTreeFiles {
		t.Errorf("directory map should be capped at %d files, got %d", maxDirTreeFiles, count)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
