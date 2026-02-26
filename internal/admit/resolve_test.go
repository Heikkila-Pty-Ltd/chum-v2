package admit

import (
	"testing"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

func testFiles() []*astpkg.ParsedFile {
	return []*astpkg.ParsedFile{
		{
			Path:    "internal/ast/parser.go",
			Package: "ast",
			Symbols: []astpkg.Symbol{
				{Name: "Parser", Kind: astpkg.KindType, Signature: "struct"},
				{Name: "NewParser", Kind: astpkg.KindFunc, Signature: "func NewParser(logger) *Parser"},
				{Name: "ParseDir", Kind: astpkg.KindMethod, Receiver: "Parser"},
				{Name: "ParseFile", Kind: astpkg.KindMethod, Receiver: "Parser"},
			},
		},
		{
			Path:    "internal/dag/dag.go",
			Package: "dag",
			Symbols: []astpkg.Symbol{
				{Name: "DAG", Kind: astpkg.KindType, Signature: "struct"},
				{Name: "GetReadyNodes", Kind: astpkg.KindMethod, Receiver: "DAG"},
				{Name: "AddEdge", Kind: astpkg.KindMethod, Receiver: "DAG"},
			},
		},
	}
}

func TestBuildSymbolIndex(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())

	if _, ok := idx.byName["Parser"]; !ok {
		t.Error("expected Parser in byName")
	}
	if _, ok := idx.byName["DAG"]; !ok {
		t.Error("expected DAG in byName")
	}
	if _, ok := idx.byFile["parser.go"]; !ok {
		t.Error("expected parser.go in byFile")
	}
	if _, ok := idx.byFile["dag.go"]; !ok {
		t.Error("expected dag.go in byFile")
	}
}

func TestResolveTargets_SymbolMatch(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	task := dag.Task{
		ID:          "test-1",
		Description: "Modify the ParseDir method on Parser to skip vendor directories.",
		Acceptance:  "Calling ParseDir excludes vendor/ files.",
	}

	targets := ResolveTargets(task, idx)
	if len(targets) == 0 {
		t.Fatal("expected targets, got none")
	}

	found := make(map[string]bool)
	for _, tgt := range targets {
		found[tgt.SymbolName] = true
	}
	if !found["ParseDir"] {
		t.Error("expected ParseDir in targets")
	}
	if !found["Parser"] {
		t.Error("expected Parser in targets")
	}
}

func TestResolveTargets_FileMatch(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	task := dag.Task{
		ID:          "test-2",
		Description: "Update the implementation in dag.go to add a new method for conflict fencing.",
		Acceptance:  "New method exists in dag.go.",
	}

	targets := ResolveTargets(task, idx)
	foundFile := false
	for _, tgt := range targets {
		if tgt.FilePath == "internal/dag/dag.go" && tgt.SymbolName == "" {
			foundFile = true
		}
	}
	if !foundFile {
		t.Error("expected file-level target for dag.go")
	}
}

func TestResolveTargets_StopwordsSkipped(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	task := dag.Task{
		ID:          "test-3",
		Description: "Add error handling and update the return type for this function implementation context.",
		Acceptance:  "The function should not panic on nil input.",
	}

	targets := ResolveTargets(task, idx)
	for _, tgt := range targets {
		lower := tgt.SymbolName
		if isStopword(lower) {
			t.Errorf("stopword %q should not appear in targets", lower)
		}
	}
}

func TestResolveTargets_ShortTokensSkipped(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	// Add a short symbol name to the index
	files := []*astpkg.ParsedFile{
		{
			Path:    "internal/x.go",
			Package: "x",
			Symbols: []astpkg.Symbol{
				{Name: "Do", Kind: astpkg.KindFunc},
			},
		},
	}
	idx = BuildSymbolIndex(files)
	task := dag.Task{
		ID:          "test-4",
		Description: "We need to Do something about this, because it is broken and needs fixing right now.",
		Acceptance:  "Do is called correctly.",
	}

	targets := ResolveTargets(task, idx)
	for _, tgt := range targets {
		if tgt.SymbolName == "Do" {
			t.Error("short symbol 'Do' should not be matched")
		}
	}
}

func TestCheckStaleness_NotStale(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	oldTargets := []dag.TaskTarget{
		{FilePath: "internal/ast/parser.go", SymbolName: "Parser", SymbolKind: "type"},
		{FilePath: "internal/ast/parser.go", SymbolName: "ParseDir", SymbolKind: "method"},
	}

	if CheckStaleness(oldTargets, idx) {
		t.Error("expected not stale, but got stale")
	}
}

func TestCheckStaleness_SymbolRemoved(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	oldTargets := []dag.TaskTarget{
		{FilePath: "internal/ast/parser.go", SymbolName: "DeletedFunction", SymbolKind: "func"},
	}

	if !CheckStaleness(oldTargets, idx) {
		t.Error("expected stale when symbol is removed")
	}
}

func TestCheckStaleness_SymbolMoved(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	oldTargets := []dag.TaskTarget{
		// Parser exists but not in dag.go
		{FilePath: "internal/dag/dag.go", SymbolName: "Parser", SymbolKind: "type"},
	}

	if !CheckStaleness(oldTargets, idx) {
		t.Error("expected stale when symbol moved to different file")
	}
}

func TestCheckStaleness_FileRemoved(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	oldTargets := []dag.TaskTarget{
		{FilePath: "internal/nonexistent/gone.go"},
	}

	if !CheckStaleness(oldTargets, idx) {
		t.Error("expected stale when file is removed")
	}
}

func TestCheckStaleness_EmptyOldTargets(t *testing.T) {
	idx := BuildSymbolIndex(testFiles())
	if CheckStaleness(nil, idx) {
		t.Error("empty old targets should not be stale")
	}
}
