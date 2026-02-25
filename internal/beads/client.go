// Package beads wraps the bd CLI for reading issues into CHUM.
// Ported from cortex/internal/beadsfork — stripped to read-only surface.
package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const DefaultBinary = "bd"

// Issue is a beads issue — the source-of-truth for task definitions.
type Issue struct {
	ID                 string       `json:"id"`
	Title              string       `json:"title"`
	Description        string       `json:"description,omitempty"`
	Status             string       `json:"status"`
	Priority           int          `json:"priority"`
	IssueType          string       `json:"issue_type"`
	Labels             []string     `json:"labels,omitempty"`
	Dependencies       []Dependency `json:"dependencies,omitempty"`
	AcceptanceCriteria string       `json:"acceptance_criteria,omitempty"`
	Design             string       `json:"design,omitempty"`
}

// Dependency represents an issue dependency edge.
type Dependency struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

// Client is a local wrapper around the bd CLI.
type Client struct {
	binary  string
	workDir string
	flags   []string
}

// NewClient creates a beads client pointing at a project directory.
func NewClient(workDir string) (*Client, error) {
	if workDir == "" {
		return nil, errors.New("workdir is required")
	}
	if _, err := exec.LookPath(DefaultBinary); err != nil {
		return nil, fmt.Errorf("bd binary not found on PATH: %w", err)
	}
	return &Client{
		binary:  DefaultBinary,
		workDir: workDir,
		flags:   []string{"--no-daemon", "--no-auto-import", "--no-auto-flush"},
	}, nil
}

// List returns all issues from bd.
func (c *Client) List(ctx context.Context, limit int) ([]Issue, error) {
	args := []string{"list", "--json"}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return decodeIssueList(out)
}

// Ready returns unblocked ready issues.
func (c *Client) Ready(ctx context.Context, limit int) ([]Issue, error) {
	args := []string{"ready", "--json"}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return decodeIssueList(out)
}

// Show returns details for one issue.
func (c *Client) Show(ctx context.Context, issueID string) (Issue, error) {
	out, err := c.run(ctx, "show", "--json", issueID)
	if err != nil {
		return Issue{}, err
	}
	return decodeSingleIssue(out)
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	fullArgs := make([]string, 0, len(c.flags)+len(args))
	fullArgs = append(fullArgs, c.flags...)
	fullArgs = append(fullArgs, args...)

	cmd := exec.CommandContext(ctx, c.binary, fullArgs...)
	cmd.Dir = c.workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w (%s)",
			c.binary, strings.Join(args, " "), err, compact(out))
	}
	return out, nil
}

// --- JSON decoders (handle bd's mixed output) ---

func decodeSingleIssue(out []byte) (Issue, error) {
	payload := extractJSON(out)
	if payload == nil {
		return Issue{}, fmt.Errorf("no JSON in bd output: %s", compact(out))
	}
	payload = []byte(strings.TrimSpace(string(payload)))
	if len(payload) == 0 {
		return Issue{}, fmt.Errorf("empty JSON payload")
	}
	switch payload[0] {
	case '{':
		var issue Issue
		if err := json.Unmarshal(payload, &issue); err != nil {
			return Issue{}, fmt.Errorf("decode issue: %w", err)
		}
		return issue, nil
	case '[':
		var list []Issue
		if err := json.Unmarshal(payload, &list); err != nil {
			return Issue{}, fmt.Errorf("decode issue list: %w", err)
		}
		if len(list) == 0 {
			return Issue{}, errors.New("empty issue list")
		}
		return list[0], nil
	default:
		return Issue{}, fmt.Errorf("unexpected JSON: %s", compact(payload))
	}
}

func decodeIssueList(out []byte) ([]Issue, error) {
	payload := extractJSON(out)
	if payload == nil {
		return nil, fmt.Errorf("no JSON in bd output: %s", compact(out))
	}
	var list []Issue
	if err := json.Unmarshal(payload, &list); err != nil {
		return nil, fmt.Errorf("decode issue list: %w", err)
	}
	return list, nil
}

func extractJSON(out []byte) []byte {
	s := strings.TrimSpace(string(out))
	if json.Valid([]byte(s)) {
		return []byte(s)
	}
	// Find first { or [ and try to parse from there
	for i := 0; i < len(s); i++ {
		if s[i] != '{' && s[i] != '[' {
			continue
		}
		candidate := strings.TrimSpace(s[i:])
		if json.Valid([]byte(candidate)) {
			return []byte(candidate)
		}
	}
	return nil
}

func compact(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "no output"
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
