// Package beads wraps the bd CLI for reading and writing issues in CHUM.
package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/jsonutil"
)

// DefaultBinary is the default bd CLI binary name.
// Override with BD_BINARY env var if multiple versions are installed.
var DefaultBinary = defaultBinaryPath()

func defaultBinaryPath() string {
	if v := os.Getenv("BD_BINARY"); v != "" {
		return v
	}
	return "bd"
}

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
	EstimatedMinutes   int          `json:"estimated_minutes,omitempty"`
}

// Dependency represents an issue dependency edge.
type Dependency struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

// Client is a local wrapper around the bd CLI.
type Client struct {
	binary   string
	workDir  string
	flags    []string
	readOnly bool
}

// NewClient creates a read-write beads client pointing at a project directory.
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
	}, nil
}

// NewReadOnlyClient creates a sandboxed beads client that cannot modify issues.
func NewReadOnlyClient(workDir string) (*Client, error) {
	c, err := NewClient(workDir)
	if err != nil {
		return nil, err
	}
	c.readOnly = true
	c.flags = []string{"--sandbox"}
	return c, nil
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

// Close closes an issue in beads with an optional reason.
func (c *Client) Close(ctx context.Context, issueID, reason string) error {
	if c.readOnly {
		return errors.New("beads client is read-only")
	}
	args := []string{"close", issueID}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	_, err := c.run(ctx, args...)
	return err
}

// CreateParams holds parameters for creating a new beads issue.
type CreateParams struct {
	Title       string
	Description string
	IssueType   string
	Priority    int // -1 means unset (use CLI default); 0+ passed as -p flag
	Labels      []string
	ParentID    string
}

// Create creates a new issue in beads and returns its ID.
func (c *Client) Create(ctx context.Context, params CreateParams) (string, error) {
	if c.readOnly {
		return "", errors.New("beads client is read-only")
	}
	args := []string{"create", params.Title, "--json"}
	if params.Description != "" {
		args = append(args, "-d", params.Description)
	}
	if params.IssueType != "" {
		args = append(args, "-t", params.IssueType)
	}
	if params.Priority >= 0 {
		args = append(args, "-p", strconv.Itoa(params.Priority))
	}
	if params.ParentID != "" {
		args = append(args, "--parent", params.ParentID)
	}
	if len(params.Labels) > 0 {
		args = append(args, "--labels", strings.Join(params.Labels, ","))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	// Extract the issue ID from the JSON output
	issue, err := decodeSingleIssue(out)
	if err != nil {
		return "", fmt.Errorf("parse created issue: %w", err)
	}
	return issue.ID, nil
}

// Update updates fields on an issue in beads.
// Supported keys: "status", "title", "description", "acceptance", "priority", "estimate".
func (c *Client) Update(ctx context.Context, issueID string, fields map[string]string) error {
	if c.readOnly {
		return errors.New("beads client is read-only")
	}
	args := []string{"update", issueID}
	for k, v := range fields {
		switch k {
		case "status":
			args = append(args, "--status", v)
		case "title":
			args = append(args, "--title", v)
		case "description":
			args = append(args, "-d", v)
		case "acceptance":
			args = append(args, "--acceptance", v)
		case "priority":
			args = append(args, "--priority", v)
		case "estimate":
			args = append(args, "--estimate", v)
		default:
			return fmt.Errorf("unsupported beads update field: %s", k)
		}
	}
	_, err := c.run(ctx, args...)
	return err
}

// Children returns child issues of the given parent.
func (c *Client) Children(ctx context.Context, parentID string) ([]Issue, error) {
	out, err := c.run(ctx, "list", "--json", "--parent", parentID)
	if err != nil {
		return nil, err
	}
	return decodeIssueList(out)
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
	return jsonutil.ExtractJSON(out)
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
