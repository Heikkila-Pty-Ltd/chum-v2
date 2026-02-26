package ast

import (
	"fmt"
	"strings"
)

// SymbolKind classifies a top-level Go declaration.
type SymbolKind string

const (
	KindFunc      SymbolKind = "func"
	KindMethod    SymbolKind = "method"
	KindType      SymbolKind = "type"
	KindInterface SymbolKind = "interface"
	KindConst     SymbolKind = "const"
	KindVar       SymbolKind = "var"
)

// Symbol represents a single top-level declaration extracted from a Go file.
type Symbol struct {
	Name       string     `json:"name"`
	Kind       SymbolKind `json:"kind"`
	Signature  string     `json:"signature"`
	Receiver   string     `json:"receiver,omitempty"`
	DocComment string     `json:"doc_comment,omitempty"`
	StartLine  int        `json:"start_line"`
	EndLine    int        `json:"end_line"`
}

// String returns a one-line summary like "func NewParser() (*Parser, error) [L10-L25]".
func (s Symbol) String() string {
	var b strings.Builder
	if s.Receiver != "" {
		fmt.Fprintf(&b, "(%s) ", s.Receiver)
	}
	b.WriteString(s.Signature)
	fmt.Fprintf(&b, " [L%d-L%d]", s.StartLine, s.EndLine)
	return b.String()
}

// ParsedFile holds the structured parse result for a single Go source file.
type ParsedFile struct {
	Path    string   `json:"path"`
	Package string   `json:"package"`
	Imports []string `json:"imports,omitempty"`
	Symbols []Symbol `json:"symbols,omitempty"`
	lines   []string // source lines (unexported, not serialized)
}

// Summary returns a compact multi-line string for LLM context injection.
// Shows signatures only — use DetailedSummary for full function bodies.
func (pf *ParsedFile) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "== %s (package %s) ==\n", pf.Path, pf.Package)
	if len(pf.Imports) > 0 {
		fmt.Fprintf(&b, "imports: %s\n", strings.Join(pf.Imports, ", "))
	}
	for _, sym := range pf.Symbols {
		b.WriteString(sym.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// DetailedSummary returns the file header plus full source for each symbol.
// Used for target files the agent is about to modify — gives complete context.
func (pf *ParsedFile) DetailedSummary() string {
	if len(pf.lines) == 0 {
		return pf.Summary() // fallback if source not available
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== %s (package %s) ==\n", pf.Path, pf.Package)
	if len(pf.Imports) > 0 {
		fmt.Fprintf(&b, "imports: %s\n", strings.Join(pf.Imports, ", "))
	}
	for _, sym := range pf.Symbols {
		b.WriteString(sym.String())
		b.WriteByte('\n')
		// Include the actual source lines for this symbol
		start := sym.StartLine - 1 // 0-indexed
		end := sym.EndLine         // exclusive
		if start < 0 {
			start = 0
		}
		if end > len(pf.lines) {
			end = len(pf.lines)
		}
		for i := start; i < end; i++ {
			fmt.Fprintf(&b, "  %d: %s\n", i+1, pf.lines[i])
		}
		b.WriteByte('\n')
	}
	return b.String()
}
