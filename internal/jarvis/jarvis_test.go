package jarvis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

func testDAG(t *testing.T) *dag.DAG {
	t.Helper()
	d, err := dag.Open(":memory:")
	if err != nil {
		t.Fatalf("open dag: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

type stubBeadsStore struct {
	nextID  int
	issues  map[string]beads.Issue
	created []beads.CreateParams
}

func newStubBeadsStore() *stubBeadsStore {
	return &stubBeadsStore{
		issues: make(map[string]beads.Issue),
	}
}

func (s *stubBeadsStore) List(_ context.Context, _ int) ([]beads.Issue, error) {
	out := make([]beads.Issue, 0, len(s.issues))
	for _, issue := range s.issues {
		out = append(out, issue)
	}
	return out, nil
}

func (s *stubBeadsStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) {
	var out []beads.Issue
	for _, issue := range s.issues {
		if issue.Status == "ready" {
			out = append(out, issue)
		}
	}
	return out, nil
}

func (s *stubBeadsStore) Show(_ context.Context, issueID string) (beads.Issue, error) {
	issue, ok := s.issues[issueID]
	if !ok {
		return beads.Issue{}, fmt.Errorf("issue %s not found", issueID)
	}
	return issue, nil
}

func (s *stubBeadsStore) Close(_ context.Context, issueID, _ string) error {
	issue, ok := s.issues[issueID]
	if !ok {
		return fmt.Errorf("issue %s not found", issueID)
	}
	issue.Status = "done"
	s.issues[issueID] = issue
	return nil
}

func (s *stubBeadsStore) Create(_ context.Context, params beads.CreateParams) (string, error) {
	s.nextID++
	id := fmt.Sprintf("bd-%d", s.nextID)
	s.created = append(s.created, params)
	s.issues[id] = beads.Issue{
		ID:               id,
		Title:            params.Title,
		Description:      params.Description,
		Status:           "open",
		Priority:         params.Priority,
		IssueType:        params.IssueType,
		Labels:           append([]string(nil), params.Labels...),
		EstimatedMinutes: params.EstimatedMinutes,
	}
	return id, nil
}

func (s *stubBeadsStore) Update(_ context.Context, issueID string, fields map[string]string) error {
	issue, ok := s.issues[issueID]
	if !ok {
		return fmt.Errorf("issue %s not found", issueID)
	}
	if v, ok := fields["status"]; ok {
		issue.Status = v
	}
	if v, ok := fields["title"]; ok {
		issue.Title = v
	}
	if v, ok := fields["description"]; ok {
		issue.Description = v
	}
	s.issues[issueID] = issue
	return nil
}

func (s *stubBeadsStore) Children(_ context.Context, parentID string) ([]beads.Issue, error) {
	var children []beads.Issue
	for _, issue := range s.issues {
		for _, created := range s.created {
			if created.ParentID == parentID && issue.Title == created.Title {
				children = append(children, issue)
				break
			}
		}
	}
	return children, nil
}

func (s *stubBeadsStore) AddDependency(_ context.Context, issueID, dependsOnID string) error {
	issue, ok := s.issues[issueID]
	if !ok {
		return fmt.Errorf("issue %s not found", issueID)
	}
	issue.Dependencies = append(issue.Dependencies, beads.Dependency{
		IssueID:     issueID,
		DependsOnID: dependsOnID,
		Type:        "blocks",
	})
	s.issues[issueID] = issue
	return nil
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if strings.EqualFold(strings.TrimSpace(label), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func TestSubmitCreatesTask(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	ctx := context.Background()
	id, err := e.Submit(ctx, WorkRequest{
		Title:       "Fix broken test",
		Description: "The TestFoo test is failing due to nil pointer",
		Project:     "chum",
		Source:      "jarvis-test",
		Labels:      []string{"bugfix"},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty task ID")
	}

	// Verify task in DAG.
	task, err := d.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Title != "Fix broken test" {
		t.Errorf("title = %q, want %q", task.Title, "Fix broken test")
	}
	if task.Status != "approved" {
		t.Errorf("status = %q, want %q", task.Status, "ready")
	}
	if task.Metadata["source"] != "jarvis-test" {
		t.Errorf("metadata[source] = %q, want %q", task.Metadata["source"], "jarvis-test")
	}

	// Should have jarvis-submitted label.
	hasLabel := false
	for _, l := range task.Labels {
		if l == "jarvis-submitted" {
			hasLabel = true
			break
		}
	}
	if !hasLabel {
		t.Error("missing jarvis-submitted label")
	}
}

func TestSubmitUnknownProject(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	_, err := e.Submit(context.Background(), WorkRequest{
		Title:   "Bad project",
		Project: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestSubmitDefaultSource(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	id, err := e.Submit(context.Background(), WorkRequest{
		Title:       "Test default source",
		Description: "desc",
		Project:     "chum",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	task, err := d.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Metadata["source"] != "jarvis" {
		t.Errorf("default source = %q, want %q", task.Metadata["source"], "jarvis")
	}
}

func TestSubmitBeadsIngressCreatesMappedTaskWithExternalRef(t *testing.T) {
	d := testDAG(t)
	bc := newStubBeadsStore()
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	e.ConfigureBeadsIngress("beads_only", "chum-canary", map[string]beads.Store{"chum": bc})

	id, err := e.Submit(context.Background(), WorkRequest{
		Title:       "Bridge this external work",
		Description: "Ensure submit routes through beads",
		Project:     "chum",
		Source:      "jarvis-webhook",
		ExternalRef: "ext-abc-123",
		Labels:      []string{"integration"},
		Priority:    1,
	})
	if err != nil {
		t.Fatalf("submit via beads: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty task ID")
	}

	task, err := d.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Metadata["source"] != "jarvis-webhook" {
		t.Fatalf("metadata[source] = %q, want jarvis-webhook", task.Metadata["source"])
	}
	if task.Metadata["external_ref"] != "ext-abc-123" {
		t.Fatalf("metadata[external_ref] = %q, want ext-abc-123", task.Metadata["external_ref"])
	}
	if task.Metadata["beads_issue_id"] != id {
		t.Fatalf("metadata[beads_issue_id] = %q, want %q", task.Metadata["beads_issue_id"], id)
	}
	if task.Metadata["beads_bridge"] != "true" {
		t.Fatalf("metadata[beads_bridge] = %q, want true", task.Metadata["beads_bridge"])
	}
	if task.Status != "approved" {
		t.Fatalf("task status = %q, want approved", task.Status)
	}
	if !hasLabel(task.Labels, "jarvis-submitted") {
		t.Fatalf("task labels missing jarvis-submitted: %#v", task.Labels)
	}
	if !hasLabel(task.Labels, "chum-canary") {
		t.Fatalf("task labels missing chum-canary: %#v", task.Labels)
	}

	mapping, err := d.GetBeadsMappingByTask(context.Background(), "chum", id)
	if err != nil {
		t.Fatalf("get beads mapping: %v", err)
	}
	if mapping.IssueID != id {
		t.Fatalf("mapping issue id = %q, want %q", mapping.IssueID, id)
	}

	if len(bc.created) != 1 {
		t.Fatalf("created issues = %d, want 1", len(bc.created))
	}
	if !hasLabel(bc.created[0].Labels, "jarvis-submitted") {
		t.Fatalf("created issue labels missing jarvis-submitted: %#v", bc.created[0].Labels)
	}
	if !hasLabel(bc.created[0].Labels, "chum-canary") {
		t.Fatalf("created issue labels missing chum-canary: %#v", bc.created[0].Labels)
	}

	issue, err := bc.Show(context.Background(), id)
	if err != nil {
		t.Fatalf("show issue: %v", err)
	}
	if issue.Status != "approved" {
		t.Fatalf("issue status = %q, want approved", issue.Status)
	}
}

func TestSubmitBeadsIngressRequiresClient(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	e.ConfigureBeadsIngress("beads_only", "chum-canary", nil)

	_, err := e.Submit(context.Background(), WorkRequest{
		Title:   "Should fail",
		Project: "chum",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "requires a beads client") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetStatusReady(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	id, err := e.Submit(context.Background(), WorkRequest{
		Title:   "Status check",
		Project: "chum",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	result, err := e.GetStatus(context.Background(), id)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if result.Status != "approved" {
		t.Errorf("status = %q, want %q", result.Status, "ready")
	}
	if result.TaskID != id {
		t.Errorf("task_id = %q, want %q", result.TaskID, id)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	_, err := e.GetStatus(context.Background(), "nonexistent-12345")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestListPendingFiltersJarvisTasks(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	ctx := context.Background()

	// Create a Jarvis task.
	_, err := e.Submit(ctx, WorkRequest{
		Title:   "Jarvis task",
		Project: "chum",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Create a non-Jarvis task directly.
	_, err = d.CreateTask(ctx, dag.Task{
		Title:   "Manual task",
		Project: "chum",
		Status:  "ready",
	})
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}

	pending, err := e.ListPending(ctx, "chum")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	if len(pending) != 1 {
		t.Fatalf("expected 1 pending jarvis task, got %d", len(pending))
	}
	if pending[0].Status != "approved" {
		t.Errorf("status = %q, want %q", pending[0].Status, "ready")
	}
}

func TestSubmitWithPriority(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	id, err := e.Submit(context.Background(), WorkRequest{
		Title:    "High priority fix",
		Project:  "chum",
		Priority: 1,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	task, err := d.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Priority != 1 {
		t.Errorf("priority = %d, want 1", task.Priority)
	}
}

func TestTriggerDispatchWithoutTemporal(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	err := e.TriggerDispatch(context.Background())
	if err == nil {
		t.Fatal("expected error when temporal client is nil")
	}
}
