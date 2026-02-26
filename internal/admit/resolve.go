package admit

import (
	"path/filepath"
	"strings"
	"unicode"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// TargetHit records where a symbol was found in the codebase.
type TargetHit struct {
	FilePath   string
	SymbolName string
	SymbolKind string
}

// SymbolIndex is a lookup structure built from parsed AST files.
type SymbolIndex struct {
	byName map[string][]TargetHit // symbol name → locations
	byFile map[string]string      // basename → full path
}

// BuildSymbolIndex constructs an index from parsed AST files.
func BuildSymbolIndex(files []*astpkg.ParsedFile) *SymbolIndex {
	idx := &SymbolIndex{
		byName: make(map[string][]TargetHit),
		byFile: make(map[string]string),
	}
	for _, f := range files {
		base := filepath.Base(f.Path)
		idx.byFile[base] = f.Path
		idx.byFile[f.Path] = f.Path

		for _, sym := range f.Symbols {
			idx.byName[sym.Name] = append(idx.byName[sym.Name], TargetHit{
				FilePath:   f.Path,
				SymbolName: sym.Name,
				SymbolKind: string(sym.Kind),
			})
		}
	}
	return idx
}

// ResolveTargets scans a task's description and acceptance criteria for
// references to symbols or files in the index.
func ResolveTargets(task dag.Task, index *SymbolIndex) []dag.TaskTarget {
	text := task.Description + "\n" + task.Acceptance
	tokens := tokenize(text)

	seen := make(map[string]bool) // dedup key: "file|symbol"
	var targets []dag.TaskTarget

	for _, tok := range tokens {
		if isStopword(tok) || len(tok) < 3 {
			continue
		}

		// Check symbol names
		if hits, ok := index.byName[tok]; ok {
			for _, h := range hits {
				key := h.FilePath + "|" + h.SymbolName
				if seen[key] {
					continue
				}
				seen[key] = true
				targets = append(targets, dag.TaskTarget{
					TaskID:     task.ID,
					FilePath:   h.FilePath,
					SymbolName: h.SymbolName,
					SymbolKind: h.SymbolKind,
				})
			}
		}

		// Check file names
		if fullPath, ok := index.byFile[tok]; ok {
			key := fullPath + "|"
			if !seen[key] {
				seen[key] = true
				targets = append(targets, dag.TaskTarget{
					TaskID:   task.ID,
					FilePath: fullPath,
				})
			}
		}
	}

	return targets
}

// CheckStaleness returns true if any of the old targets no longer exist
// in the current symbol index.
func CheckStaleness(oldTargets []dag.TaskTarget, index *SymbolIndex) bool {
	for _, old := range oldTargets {
		if old.SymbolName != "" {
			hits, ok := index.byName[old.SymbolName]
			if !ok {
				return true // symbol gone entirely
			}
			// Check it's still in the same file
			found := false
			for _, h := range hits {
				if h.FilePath == old.FilePath {
					found = true
					break
				}
			}
			if !found {
				return true // symbol moved or removed from this file
			}
		} else {
			// File-level reference
			if _, ok := index.byFile[old.FilePath]; !ok {
				return true // file gone
			}
		}
	}
	return false
}

// tokenize splits text into identifier-like tokens.
func tokenize(text string) []string {
	var tokens []string
	var cur strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' {
			cur.WriteRune(r)
		} else {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// stopwords are common Go keywords and builtins that should not be matched
// as symbol references.
var stopwords = map[string]bool{
	// Go keywords
	"break": true, "case": true, "chan": true, "const": true,
	"continue": true, "default": true, "defer": true, "else": true,
	"fallthrough": true, "for": true, "func": true, "goto": true,
	"if": true, "import": true, "interface": true, "map": true,
	"package": true, "range": true, "return": true, "select": true,
	"struct": true, "switch": true, "type": true, "var": true,
	// Builtins
	"append": true, "cap": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true,
	"make": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
	// Common types
	"bool": true, "byte": true, "error": true, "int": true,
	"int8": true, "int16": true, "int32": true, "int64": true,
	"float32": true, "float64": true, "string": true, "rune": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true,
	"uint64": true, "uintptr": true, "any": true,
	// Common identifiers
	"nil": true, "true": true, "false": true, "iota": true,
	"ctx": true, "err": true, "fmt": true, "log": true,
	"the": true, "and": true, "not": true,
	"with": true, "that": true, "this": true, "from": true,
	"all": true, "are": true, "has": true, "was": true,
	"will": true, "can": true, "should": true, "must": true,
	"add": true, "fix": true, "update": true, "remove": true,
}

func isStopword(s string) bool {
	return stopwords[strings.ToLower(s)]
}
