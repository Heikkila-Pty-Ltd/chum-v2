package ast

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

// cacheEntry holds a cached parse result keyed by mtime.
type cacheEntry struct {
	mtime  time.Time
	result *ParsedFile
}

// Parser wraps a tree-sitter parser with mtime-based caching.
// It is safe for concurrent use.
type Parser struct {
	parser *sitter.Parser
	mu     sync.Mutex // protects parser (tree-sitter parsers are not thread-safe)
	cache  sync.Map   // map[string]cacheEntry
	logger *slog.Logger
}

// NewParser creates a Parser configured for Go source files.
func NewParser(logger *slog.Logger) *Parser {
	p := sitter.NewParser()
	p.SetLanguage(golang.GetLanguage())
	if logger == nil {
		logger = slog.Default()
	}
	return &Parser{
		parser: p,
		logger: logger,
	}
}

// Close releases the underlying tree-sitter parser.
func (p *Parser) Close() {
	p.parser.Close()
}

// ParseFile parses a single Go source file and returns structured results.
// Results are cached by file path and mtime; unchanged files return cached data.
func (p *Parser) ParseFile(ctx context.Context, path string) (*ParsedFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	mtime := info.ModTime()

	// Check cache
	if cached, ok := p.cache.Load(path); ok {
		entry, valid := cached.(cacheEntry)
		if valid && entry.mtime.Equal(mtime) {
			return entry.result, nil
		}
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Parse with tree-sitter (serialized — parser is not thread-safe)
	p.mu.Lock()
	tree, err := p.parser.ParseCtx(ctx, nil, src)
	p.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	defer tree.Close()

	root := tree.RootNode()
	pkg, imports, symbols := extractFile(root, src)

	result := &ParsedFile{
		Path:    path,
		Package: pkg,
		Imports: imports,
		Symbols: symbols,
		lines:   strings.Split(string(src), "\n"),
	}

	p.cache.Store(path, cacheEntry{mtime: mtime, result: result})
	return result, nil
}

// skipDirs are directory names to skip during directory walking.
var skipDirs = map[string]bool{
	"vendor":       true,
	"testdata":     true,
	".git":         true,
	"node_modules": true,
}

// ParseDir recursively parses all .go files in dir.
// Skips vendor, testdata, .git, and node_modules directories.
// Files that fail to parse are logged and skipped.
func (p *Parser) ParseDir(ctx context.Context, dir string) ([]*ParsedFile, error) {
	var results []*ParsedFile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		pf, parseErr := p.ParseFile(ctx, path)
		if parseErr != nil {
			p.logger.Warn("Skipping unparseable file", "Path", path, "Error", parseErr)
			return nil
		}
		results = append(results, pf)
		return nil
	})
	if err != nil {
		return results, fmt.Errorf("walk %s: %w", dir, err)
	}
	return results, nil
}

// ParseFiles parses specific files by path. Files that fail to parse are
// logged and skipped.
func (p *Parser) ParseFiles(ctx context.Context, workDir string, paths []string) []*ParsedFile {
	var results []*ParsedFile
	for _, rel := range paths {
		abs := rel
		if !filepath.IsAbs(rel) {
			abs = filepath.Join(workDir, rel)
		}
		pf, err := p.ParseFile(ctx, abs)
		if err != nil {
			p.logger.Warn("Skipping unparseable file", "Path", rel, "Error", err)
			continue
		}
		// Store the relative path for cleaner prompt output
		pf.Path = rel
		results = append(results, pf)
	}
	return results
}

// Summarize produces a compact multi-line context string from parsed files.
// This is the primary output format for LLM prompt injection.
func Summarize(files []*ParsedFile) string {
	var b strings.Builder
	for _, f := range files {
		if len(f.Symbols) == 0 && len(f.Imports) == 0 {
			continue
		}
		b.WriteString(f.Summary())
		b.WriteByte('\n')
	}
	return b.String()
}

// SummarizeTargeted produces context with full source for target files and
// signatures-only for surrounding files. This gives agents deep understanding
// of files they're about to modify while maintaining awareness of the broader
// codebase.
func SummarizeTargeted(allFiles []*ParsedFile, targetFiles []*ParsedFile) string {
	targetPaths := make(map[string]bool, len(targetFiles))
	for _, f := range targetFiles {
		targetPaths[f.Path] = true
	}

	var b strings.Builder

	// Target files first — detailed with full source
	if len(targetFiles) > 0 {
		b.WriteString("=== FILES TO MODIFY (full source) ===\n\n")
		for _, f := range targetFiles {
			b.WriteString(f.DetailedSummary())
			b.WriteByte('\n')
		}
	}

	// Surrounding files — signatures only
	var hasContext bool
	for _, f := range allFiles {
		if targetPaths[f.Path] || (len(f.Symbols) == 0 && len(f.Imports) == 0) {
			continue
		}
		if !hasContext {
			b.WriteString("=== SURROUNDING CODEBASE (signatures only) ===\n\n")
			hasContext = true
		}
		b.WriteString(f.Summary())
		b.WriteByte('\n')
	}

	return b.String()
}
