package ast

import (
	"strings"
	"unicode"
)

// FilterRelevant splits parsed files into relevant (high-signal for the task)
// and surrounding (low-signal but useful for context). The task prompt is
// tokenized into keywords which are matched against file paths, package names,
// symbol names, receivers, and doc comments. Files scoring above the threshold
// are considered relevant.
//
// This enables targeted context injection: relevant files get full source in
// the prompt while surrounding files get signatures only, dramatically reducing
// token burn.
func FilterRelevant(taskPrompt string, files []*ParsedFile) (relevant, surrounding []*ParsedFile) {
	keywords := extractKeywords(taskPrompt)
	if len(keywords) == 0 {
		// Can't filter without keywords — return everything as surrounding
		return nil, files
	}

	for _, f := range files {
		score := scoreFile(f, keywords)
		if score > 0 {
			relevant = append(relevant, f)
		} else {
			surrounding = append(surrounding, f)
		}
	}

	// If we matched too many files (>30% of codebase), raise the bar.
	// This prevents overly generic keywords from defeating the purpose.
	if len(files) > 10 && len(relevant) > len(files)*3/10 {
		threshold := 2
		var narrowed []*ParsedFile
		var demoted []*ParsedFile
		for _, f := range relevant {
			if scoreFile(f, keywords) >= threshold {
				narrowed = append(narrowed, f)
			} else {
				demoted = append(demoted, f)
			}
		}
		if len(narrowed) > 0 {
			relevant = narrowed
			surrounding = append(surrounding, demoted...)
		}
		// If narrowing eliminated everything, keep original relevant set
	}

	// If nothing matched at all, return everything as surrounding so the
	// agent isn't blind.
	if len(relevant) == 0 {
		return nil, files
	}

	return relevant, surrounding
}

// scoreFile counts how many distinct keywords match content in this file.
func scoreFile(f *ParsedFile, keywords []string) int {
	// Build a searchable string from the file's metadata
	var searchable strings.Builder
	searchable.WriteString(strings.ToLower(f.Path))
	searchable.WriteByte(' ')
	searchable.WriteString(strings.ToLower(f.Package))
	searchable.WriteByte(' ')
	for _, imp := range f.Imports {
		searchable.WriteString(strings.ToLower(imp))
		searchable.WriteByte(' ')
	}
	for _, sym := range f.Symbols {
		searchable.WriteString(strings.ToLower(sym.Name))
		searchable.WriteByte(' ')
		searchable.WriteString(strings.ToLower(sym.Signature))
		searchable.WriteByte(' ')
		if sym.Receiver != "" {
			searchable.WriteString(strings.ToLower(sym.Receiver))
			searchable.WriteByte(' ')
		}
		if sym.DocComment != "" {
			searchable.WriteString(strings.ToLower(sym.DocComment))
			searchable.WriteByte(' ')
		}
	}
	text := searchable.String()

	hits := 0
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			hits++
		}
	}
	return hits
}

// extractKeywords tokenizes a task prompt into lowercase keywords suitable
// for matching against code symbols. Splits on whitespace and punctuation,
// handles camelCase/PascalCase splitting, and filters out stop words and
// very short tokens.
func extractKeywords(prompt string) []string {
	// First split on whitespace and common delimiters (preserve case for camelCase splitting)
	rawWords := strings.FieldsFunc(prompt, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '.' || r == ':' ||
			r == ';' || r == '(' || r == ')' || r == '{' || r == '}' ||
			r == '[' || r == ']' || r == '"' || r == '\'' || r == '`' ||
			r == '/' || r == '\\' || r == '|' || r == '!' || r == '?'
	})

	// Also split camelCase/PascalCase tokens, then lowercase everything
	var expanded []string
	for _, w := range rawWords {
		expanded = append(expanded, strings.ToLower(w))
		// Split camelCase: "buildCodebaseContext" -> "build", "Codebase", "Context"
		parts := splitCamelCase(w)
		if len(parts) > 1 {
			for _, p := range parts {
				expanded = append(expanded, strings.ToLower(p))
			}
		}
	}

	// Deduplicate and filter
	seen := make(map[string]bool)
	var result []string
	for _, w := range expanded {
		w = strings.TrimSpace(w)
		if len(w) < 3 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		result = append(result, w)
	}
	return result
}

// splitCamelCase splits "parseTaskConfig" into ["parse", "Task", "Config"].
func splitCamelCase(s string) []string {
	var parts []string
	var current strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// stopWords are common English words that don't help with code matching.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "that": true, "this": true,
	"with": true, "from": true, "are": true, "was": true, "were": true,
	"been": true, "have": true, "has": true, "had": true, "not": true,
	"but": true, "what": true, "all": true, "can": true, "her": true,
	"his": true, "they": true, "them": true, "then": true, "than": true,
	"into": true, "only": true, "come": true, "its": true, "over": true,
	"such": true, "take": true, "other": true, "could": true, "which": true,
	"their": true, "will": true, "when": true, "who": true, "make": true,
	"like": true, "each": true, "does": true, "how": true, "should": true,
	"add": true, "use": true, "update": true, "change": true, "fix": true,
	"implement": true, "create": true, "remove": true, "delete": true,
	"new": true, "need": true, "file": true, "code": true, "function": true,
	"method": true, "type": true, "struct": true, "interface": true,
	"package": true, "import": true, "return": true, "error": true,
	"string": true, "int": true, "bool": true, "nil": true, "err": true,
}
