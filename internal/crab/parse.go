package crab

import (
	"fmt"
	"strings"
)

// ParseMarkdownPlan performs deterministic, line-by-line parsing of a
// semi-structured markdown plan into a ParsedPlan. It expects the format:
//
//	# Plan: <title>
//	## Context
//	<body text>
//	## Scope
//	- [ ] <deliverable>
//	## Acceptance Criteria
//	- <criterion>
//	## Out of Scope
//	- <item>
//
// The parser is forgiving: unknown sections are ignored, optional sections
// may be absent, and whitespace is trimmed throughout.
func ParseMarkdownPlan(markdown string) (*ParsedPlan, error) {
	plan := &ParsedPlan{
		RawMarkdown: markdown,
	}

	var (
		currentSection string
		contextLines   []string
		scopeIndex     int
	)

	lines := strings.Split(markdown, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if plan.Title == "" && isTitleLine(trimmed) {
			plan.Title = extractTitle(trimmed)
			continue
		}

		if strings.HasPrefix(trimmed, "## ") {
			section := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			currentSection = strings.ToLower(section)
			continue
		}

		if trimmed == "" {
			if currentSection == "context" {
				contextLines = append(contextLines, "")
			}
			continue
		}

		switch currentSection {
		case "context":
			contextLines = append(contextLines, trimmed)

		case "scope":
			item, ok := parseScopeItem(trimmed, scopeIndex)
			if ok {
				plan.ScopeItems = append(plan.ScopeItems, item)
				scopeIndex++
			}

		case "acceptance criteria":
			if bullet, ok := parseBullet(trimmed); ok {
				plan.AcceptanceCriteria = append(plan.AcceptanceCriteria, bullet)
			}

		case "out of scope":
			if bullet, ok := parseBullet(trimmed); ok {
				plan.OutOfScope = append(plan.OutOfScope, bullet)
			}
		}
	}

	plan.Context = strings.TrimSpace(strings.Join(contextLines, "\n"))

	if err := validateParsedPlan(plan); err != nil {
		return nil, err
	}

	return plan, nil
}

func isTitleLine(line string) bool {
	if strings.HasPrefix(line, "# Plan:") {
		return true
	}
	return strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ")
}

func extractTitle(line string) string {
	if strings.HasPrefix(line, "# Plan:") {
		return strings.TrimSpace(strings.TrimPrefix(line, "# Plan:"))
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "#"))
}

func parseScopeItem(line string, index int) (ScopeItem, bool) {
	var rest string
	switch {
	case strings.HasPrefix(line, "- "):
		rest = strings.TrimPrefix(line, "- ")
	case strings.HasPrefix(line, "* "):
		rest = strings.TrimPrefix(line, "* ")
	default:
		return ScopeItem{}, false
	}

	completed := false
	switch {
	case strings.HasPrefix(rest, "[ ] ") || rest == "[ ]":
		rest = strings.TrimPrefix(rest, "[ ]")
	case strings.HasPrefix(rest, "[x] ") || rest == "[x]" ||
		strings.HasPrefix(rest, "[X] ") || rest == "[X]":
		completed = true
		if strings.HasPrefix(rest, "[x] ") {
			rest = strings.TrimPrefix(rest, "[x]")
		} else {
			rest = strings.TrimPrefix(rest, "[X]")
		}
	default:
		return ScopeItem{}, false
	}

	desc := strings.TrimSpace(rest)
	if desc == "" {
		return ScopeItem{}, false
	}

	return ScopeItem{
		Index:       index,
		Description: desc,
		Completed:   completed,
	}, true
}

func parseBullet(line string) (string, bool) {
	for _, prefix := range []string{"- ", "* "} {
		if strings.HasPrefix(line, prefix) {
			text := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if text != "" {
				return text, true
			}
		}
	}
	return "", false
}

func validateParsedPlan(plan *ParsedPlan) error {
	if strings.TrimSpace(plan.Title) == "" {
		return fmt.Errorf("parsed plan has no title: expected '# Plan: <title>' header")
	}
	if len(plan.ScopeItems) == 0 {
		return fmt.Errorf("parsed plan has no scope items: at least one '- [ ] <deliverable>' required")
	}
	return nil
}
