package beadsbridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
)

type submitStore struct {
	created       beads.CreateParams
	updatedID     string
	updatedFields map[string]string
	depLinks      [][2]string
	nextID        string
}

func (s *submitStore) List(_ context.Context, _ int) ([]beads.Issue, error)  { return nil, nil }
func (s *submitStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) { return nil, nil }
func (s *submitStore) Show(_ context.Context, _ string) (beads.Issue, error) {
	return beads.Issue{}, nil
}
func (s *submitStore) Close(_ context.Context, _, _ string) error { return nil }
func (s *submitStore) Create(_ context.Context, p beads.CreateParams) (string, error) {
	s.created = p
	if s.nextID == "" {
		s.nextID = "bd-1"
	}
	return s.nextID, nil
}
func (s *submitStore) Update(_ context.Context, id string, fields map[string]string) error {
	s.updatedID = id
	s.updatedFields = fields
	return nil
}
func (s *submitStore) Children(_ context.Context, _ string) ([]beads.Issue, error) { return nil, nil }
func (s *submitStore) AddDependency(_ context.Context, issueID, dependsOnID string) error {
	s.depLinks = append(s.depLinks, [2]string{issueID, dependsOnID})
	return nil
}

func TestParseSubmitMarkdown(t *testing.T) {
	t.Parallel()
	input := `---
title: Ship Beads Bridge
type: feature
priority: 1
labels: [bridge, orchestration]
estimate: 45
deps:
  - bd-2
---
# ignored because title in frontmatter

## Scope
Implement controlled admission.

## Acceptance Criteria
- starts in dry-run
- canary only

## Design
Use durable outbox.
`
	spec, err := ParseSubmitMarkdown([]byte(input))
	if err != nil {
		t.Fatalf("parse markdown: %v", err)
	}
	if spec.Title != "Ship Beads Bridge" {
		t.Fatalf("title=%q", spec.Title)
	}
	if spec.IssueType != "feature" || spec.Priority != 1 {
		t.Fatalf("unexpected type/priority: %s/%d", spec.IssueType, spec.Priority)
	}
	if spec.Description == "" || spec.Acceptance == "" || spec.Design == "" {
		t.Fatalf("expected scope/acceptance/design to be extracted: %+v", spec)
	}
	if len(spec.Dependencies) != 1 || spec.Dependencies[0] != "bd-2" {
		t.Fatalf("deps=%v", spec.Dependencies)
	}
}

func TestSubmitFromFile_Create(t *testing.T) {
	t.Parallel()
	content := `# New Work

## Scope
Do the thing.

## Acceptance Criteria
It works.
`
	dir := t.TempDir()
	path := filepath.Join(dir, "work.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	store := &submitStore{nextID: "bd-99"}
	res, err := SubmitFromFile(context.Background(), store, path)
	if err != nil {
		t.Fatalf("submit create: %v", err)
	}
	if !res.Created || res.IssueID != "bd-99" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if store.created.Title == "" {
		t.Fatal("expected create params to be populated")
	}
}

func TestSubmitFromFile_UpdateAndDeps(t *testing.T) {
	t.Parallel()
	content := `---
issue_id: bd-42
labels: [a,b]
deps: [bd-1]
---
# Existing Work

## Scope
Keep improving.

## Dependencies
- bd-2
`
	dir := t.TempDir()
	path := filepath.Join(dir, "work.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	store := &submitStore{}
	res, err := SubmitFromFile(context.Background(), store, path)
	if err != nil {
		t.Fatalf("submit update: %v", err)
	}
	if !res.Updated || res.IssueID != "bd-42" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if store.updatedID != "bd-42" {
		t.Fatalf("updatedID=%q want bd-42", store.updatedID)
	}
	if len(store.depLinks) != 2 {
		t.Fatalf("dep links=%v", store.depLinks)
	}
}
