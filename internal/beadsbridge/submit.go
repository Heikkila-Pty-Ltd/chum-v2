package beadsbridge

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"gopkg.in/yaml.v3"
)

type submitFrontMatter struct {
	IssueID  string   `yaml:"issue_id"`
	Title    string   `yaml:"title"`
	Type     string   `yaml:"type"`
	Priority int      `yaml:"priority"`
	Labels   []string `yaml:"labels"`
	Estimate int      `yaml:"estimate"`
	ParentID string   `yaml:"parent_id"`
	Deps     []string `yaml:"deps"`
}

// SubmitSpec is the normalized work item extracted from markdown.
type SubmitSpec struct {
	IssueID         string
	Title           string
	Description     string
	Acceptance      string
	Design          string
	IssueType       string
	Priority        int
	EstimateMinutes int
	Labels          []string
	ParentID        string
	Dependencies    []string
	SourceMarkdown  string
}

// SubmitResult describes a submit operation outcome.
type SubmitResult struct {
	IssueID  string
	Created  bool
	Updated  bool
	Title    string
	Deps     int
	FilePath string
}

// ParseSubmitMarkdown parses a work markdown file into a normalized SubmitSpec.
func ParseSubmitMarkdown(data []byte) (SubmitSpec, error) {
	text := string(data)
	body, fm, err := splitFrontMatter(text)
	if err != nil {
		return SubmitSpec{}, err
	}

	sections, firstH1 := parseMarkdownSections(body)
	title := strings.TrimSpace(fm.Title)
	if title == "" {
		title = strings.TrimSpace(firstH1)
	}
	if title == "" {
		return SubmitSpec{}, fmt.Errorf("missing title (frontmatter title or first H1)")
	}

	description := strings.TrimSpace(sections["scope"])
	if description == "" {
		description = strings.TrimSpace(sections["body"])
	}
	acceptance := firstNonEmptySection(sections, "acceptance criteria", "acceptance")
	design := firstNonEmptySection(sections, "design")
	deps := collectDependencies(fm.Deps, sections["dependencies"])
	labels := dedupeSortedStrings(fm.Labels)

	issueType := strings.TrimSpace(fm.Type)
	if issueType == "" {
		issueType = "task"
	}
	priority := fm.Priority
	if priority < 0 {
		priority = 2
	}

	return SubmitSpec{
		IssueID:         strings.TrimSpace(fm.IssueID),
		Title:           title,
		Description:     description,
		Acceptance:      acceptance,
		Design:          design,
		IssueType:       issueType,
		Priority:        priority,
		EstimateMinutes: fm.Estimate,
		Labels:          labels,
		ParentID:        strings.TrimSpace(fm.ParentID),
		Dependencies:    deps,
		SourceMarkdown:  body,
	}, nil
}

// SubmitFromFile creates or updates a beads issue from markdown/frontmatter.
func SubmitFromFile(ctx context.Context, client beads.Store, path string) (SubmitResult, error) {
	if client == nil {
		return SubmitResult{}, fmt.Errorf("nil beads client")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("read submit file: %w", err)
	}
	spec, err := ParseSubmitMarkdown(data)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("parse submit markdown: %w", err)
	}

	res := SubmitResult{
		Title:    spec.Title,
		FilePath: path,
		Deps:     len(spec.Dependencies),
	}
	if spec.IssueID == "" {
		issueID, err := client.Create(ctx, beads.CreateParams{
			Title:            spec.Title,
			Description:      spec.Description,
			IssueType:        spec.IssueType,
			Priority:         spec.Priority,
			Labels:           spec.Labels,
			ParentID:         spec.ParentID,
			Acceptance:       spec.Acceptance,
			Design:           spec.Design,
			EstimatedMinutes: spec.EstimateMinutes,
			Dependencies:     spec.Dependencies,
		})
		if err != nil {
			return SubmitResult{}, fmt.Errorf("create beads issue: %w", err)
		}
		res.IssueID = issueID
		res.Created = true
		return res, nil
	}

	fields := map[string]string{
		"title":       spec.Title,
		"description": spec.Description,
		"acceptance":  spec.Acceptance,
		"design":      spec.Design,
	}
	fields["priority"] = strconv.Itoa(spec.Priority)
	if spec.EstimateMinutes > 0 {
		fields["estimate"] = strconv.Itoa(spec.EstimateMinutes)
	}
	if len(spec.Labels) > 0 {
		fields["set_labels"] = strings.Join(spec.Labels, ",")
	}
	if err := client.Update(ctx, spec.IssueID, fields); err != nil {
		return SubmitResult{}, fmt.Errorf("update beads issue %s: %w", spec.IssueID, err)
	}
	for _, dep := range spec.Dependencies {
		if err := client.AddDependency(ctx, spec.IssueID, dep); err != nil {
			return SubmitResult{}, fmt.Errorf("link dependency %s -> %s: %w", spec.IssueID, dep, err)
		}
	}
	res.IssueID = spec.IssueID
	res.Updated = true
	return res, nil
}

func splitFrontMatter(content string) (string, submitFrontMatter, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---\n") {
		return content, submitFrontMatter{Priority: 2}, nil
	}
	start := strings.Index(content, "---\n")
	if start < 0 {
		return content, submitFrontMatter{Priority: 2}, nil
	}
	rest := content[start+4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", submitFrontMatter{}, fmt.Errorf("invalid frontmatter: missing closing ---")
	}
	fmRaw := rest[:end]
	body := strings.TrimSpace(rest[end+4:])
	var fm submitFrontMatter
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return "", submitFrontMatter{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	if fm.Priority == 0 {
		fm.Priority = 2
	}
	return body, fm, nil
}

func parseMarkdownSections(body string) (map[string]string, string) {
	lines := strings.Split(body, "\n")
	sections := map[string][]string{
		"body": {},
	}
	current := "body"
	firstH1 := ""
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			h := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if strings.HasPrefix(trimmed, "# ") && firstH1 == "" {
				firstH1 = h
			}
			current = strings.ToLower(h)
			if _, ok := sections[current]; !ok {
				sections[current] = []string{}
			}
			continue
		}
		sections[current] = append(sections[current], line)
	}
	out := make(map[string]string, len(sections))
	for k, v := range sections {
		out[k] = strings.TrimSpace(strings.Join(v, "\n"))
	}
	return out, firstH1
}

func firstNonEmptySection(sections map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(sections[strings.ToLower(k)]); v != "" {
			return v
		}
	}
	return ""
}

var depIDPattern = regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9_-]*-[a-zA-Z0-9]+\b`)

func collectDependencies(frontmatter []string, depSection string) []string {
	var deps []string
	for _, d := range frontmatter {
		d = strings.TrimSpace(d)
		if d != "" {
			deps = append(deps, d)
		}
	}
	for _, line := range strings.Split(depSection, "\n") {
		for _, m := range depIDPattern.FindAllString(line, -1) {
			deps = append(deps, m)
		}
	}
	return dedupeSortedStrings(deps)
}

func dedupeSortedStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		k := strings.ToLower(item)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}
