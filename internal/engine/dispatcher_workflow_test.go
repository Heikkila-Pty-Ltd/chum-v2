package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func TestDispatcherWorkflow_NoCandidates(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	env.OnActivity(da.RecordDispatchStartActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return([]DispatchCandidate{}, nil)
	env.OnActivity(da.ScanOrphanedReviewsActivity, mock.Anything).Return([]ReviewRequest{}, nil)

	dispatched := 0
	env.OnWorkflow(AgentWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		dispatched++
	}).Return(nil).Maybe()

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("unexpected workflow error: %v", err)
	}
	if dispatched != 0 {
		t.Fatalf("expected no child dispatches, got %d", dispatched)
	}
}

func TestDispatcherWorkflow_MarkTaskRunningFailureSkipsDispatch(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	env.OnActivity(da.RecordDispatchStartActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return([]DispatchCandidate{
		{
			TaskID:  "task-1",
			Project: "p",
			Prompt:  "do it",
			WorkDir: "/tmp/p",
			Agent:   "claude",
		},
	}, nil)
	env.OnActivity(da.ScanOrphanedReviewsActivity, mock.Anything).Return([]ReviewRequest{}, nil)
	env.OnActivity(da.MarkTaskRunningActivity, mock.Anything, "task-1").Return(errors.New("mark-failed"))

	dispatched := 0
	env.OnWorkflow(AgentWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		dispatched++
	}).Return(nil).Maybe()

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("unexpected workflow error: %v", err)
	}
	if dispatched != 0 {
		t.Fatalf("expected skipped dispatch when mark-running fails, got %d", dispatched)
	}
}

func TestDispatcherWorkflow_DispatchesCandidateWithExpectedRequest(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	env.OnActivity(da.RecordDispatchStartActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return([]DispatchCandidate{
		{
			TaskID:          "task-1",
			Project:         "p",
			Prompt:          "do it",
			WorkDir:         "/tmp/p",
			Agent:           "claude",
			Model:           "claude-sonnet",
			Tier:            "fast",
			MaxReviewRounds: 6,
		},
	}, nil)
	env.OnActivity(da.ScanOrphanedReviewsActivity, mock.Anything).Return([]ReviewRequest{}, nil)
	env.OnActivity(da.MarkTaskRunningActivity, mock.Anything, "task-1").Return(nil)

	dispatched := 0
	env.OnWorkflow(AgentWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		dispatched++
		req, ok := args.Get(1).(TaskRequest)
		if !ok {
			t.Fatalf("expected TaskRequest argument, got %T", args.Get(1))
		}
		if req.TaskID != "task-1" {
			t.Fatalf("TaskID = %q, want task-1", req.TaskID)
		}
		if req.Project != "p" {
			t.Fatalf("Project = %q, want p", req.Project)
		}
		if req.WorkDir != "/tmp/p" {
			t.Fatalf("WorkDir = %q, want /tmp/p", req.WorkDir)
		}
		if req.Agent != "claude" {
			t.Fatalf("Agent = %q, want claude", req.Agent)
		}
		if req.Model != "claude-sonnet" {
			t.Fatalf("Model = %q, want claude-sonnet", req.Model)
		}
		if req.Tier != "fast" {
			t.Fatalf("Tier = %q, want fast", req.Tier)
		}
		if req.MaxReviewRounds != 6 {
			t.Fatalf("MaxReviewRounds = %d, want 6", req.MaxReviewRounds)
		}
	}).Return(nil)

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("unexpected workflow error: %v", err)
	}
	if dispatched != 1 {
		t.Fatalf("expected one child dispatch, got %d", dispatched)
	}
}

func TestDispatcherWorkflow_MultipleCandidates(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	env.OnActivity(da.RecordDispatchStartActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	candidates := []DispatchCandidate{
		{
			TaskID:        "task-1",
			Project:       "test-project",
			Prompt:        "First task",
			WorkDir:       "/work/dir1",
			Agent:         "agent1",
			Model:         "model1",
			Tier:          "fast",
			ParentID:      "parent-1",
			ExecTimeout:   30 * time.Minute,
			ShortTimeout:  5 * time.Minute,
			ReviewTimeout: 10 * time.Minute,
		},
		{
			TaskID:        "task-2",
			Project:       "test-project",
			Prompt:        "Second task",
			WorkDir:       "/work/dir2",
			Agent:         "agent2",
			Model:         "model2",
			Tier:          "balanced",
			ParentID:      "parent-2",
			ExecTimeout:   45 * time.Minute,
			ShortTimeout:  7 * time.Minute,
			ReviewTimeout: 15 * time.Minute,
		},
		{
			TaskID:        "task-3",
			Project:       "test-project",
			Prompt:        "Third task",
			WorkDir:       "/work/dir3",
			Agent:         "agent3",
			Model:         "model3",
			Tier:          "premium",
			ParentID:      "parent-3",
			ExecTimeout:   60 * time.Minute,
			ShortTimeout:  10 * time.Minute,
			ReviewTimeout: 20 * time.Minute,
		},
	}

	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return(candidates, nil)
	env.OnActivity(da.ScanOrphanedReviewsActivity, mock.Anything).Return([]ReviewRequest{}, nil)

	// First mark succeeds
	env.OnActivity(da.MarkTaskRunningActivity, mock.Anything, "task-1").Return(nil)
	// Second mark fails (skip)
	env.OnActivity(da.MarkTaskRunningActivity, mock.Anything, "task-2").Return(errors.New("mark failed"))
	// Third mark succeeds
	env.OnActivity(da.MarkTaskRunningActivity, mock.Anything, "task-3").Return(nil)

	// Capture the TaskRequests passed to child workflows
	var capturedRequests []TaskRequest
	dispatched := 0
	env.OnWorkflow(AgentWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		dispatched++
		req, ok := args.Get(1).(TaskRequest)
		if !ok {
			t.Fatalf("expected TaskRequest argument, got %T", args.Get(1))
		}
		capturedRequests = append(capturedRequests, req)
	}).Return(nil)

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("unexpected workflow error: %v", err)
	}

	// Assert exactly 2 child workflows dispatched
	if dispatched != 2 {
		t.Fatalf("expected 2 child dispatches, got %d", dispatched)
	}
	if len(capturedRequests) != 2 {
		t.Fatalf("expected 2 captured requests, got %d", len(capturedRequests))
	}

	// Verify each child gets correct TaskRequest
	// Should have task-1 and task-3 (task-2 was skipped due to mark failure)

	// First dispatched should be task-1
	req1 := capturedRequests[0]
	if req1.TaskID != "task-1" {
		t.Fatalf("First request TaskID = %q, want task-1", req1.TaskID)
	}
	if req1.Project != "test-project" {
		t.Fatalf("First request Project = %q, want test-project", req1.Project)
	}
	if req1.Prompt != "First task" {
		t.Fatalf("First request Prompt = %q, want First task", req1.Prompt)
	}
	if req1.WorkDir != "/work/dir1" {
		t.Fatalf("First request WorkDir = %q, want /work/dir1", req1.WorkDir)
	}
	if req1.Agent != "agent1" {
		t.Fatalf("First request Agent = %q, want agent1", req1.Agent)
	}
	if req1.Model != "model1" {
		t.Fatalf("First request Model = %q, want model1", req1.Model)
	}
	if req1.Tier != "fast" {
		t.Fatalf("First request Tier = %q, want fast", req1.Tier)
	}
	if req1.ParentID != "parent-1" {
		t.Fatalf("First request ParentID = %q, want parent-1", req1.ParentID)
	}

	// Second dispatched should be task-3
	req3 := capturedRequests[1]
	if req3.TaskID != "task-3" {
		t.Fatalf("Second request TaskID = %q, want task-3", req3.TaskID)
	}
	if req3.Project != "test-project" {
		t.Fatalf("Second request Project = %q, want test-project", req3.Project)
	}
	if req3.Prompt != "Third task" {
		t.Fatalf("Second request Prompt = %q, want Third task", req3.Prompt)
	}
	if req3.WorkDir != "/work/dir3" {
		t.Fatalf("Second request WorkDir = %q, want /work/dir3", req3.WorkDir)
	}
	if req3.Agent != "agent3" {
		t.Fatalf("Second request Agent = %q, want agent3", req3.Agent)
	}
	if req3.Model != "model3" {
		t.Fatalf("Second request Model = %q, want model3", req3.Model)
	}
	if req3.Tier != "premium" {
		t.Fatalf("Second request Tier = %q, want premium", req3.Tier)
	}
	if req3.ParentID != "parent-3" {
		t.Fatalf("Second request ParentID = %q, want parent-3", req3.ParentID)
	}
}

func TestDispatcherWorkflow_ScanFailure(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	env.OnActivity(da.RecordDispatchStartActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return(nil, errors.New("database connection failed"))

	dispatched := 0
	env.OnWorkflow(AgentWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		dispatched++
	}).Return(nil).Maybe()

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("expected workflow error but got none")
	}
	if !strings.Contains(err.Error(), "scan") {
		t.Fatalf("expected error to contain 'scan', got: %v", err)
	}
	if dispatched != 0 {
		t.Fatalf("expected no child workflows spawned when scan fails, got %d", dispatched)
	}
}

func TestDispatcherWorkflow_NoCandidates_StillDispatchesOrphanReview(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	env.OnActivity(da.RecordDispatchStartActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return([]DispatchCandidate{}, nil)
	env.OnActivity(da.ScanOrphanedReviewsActivity, mock.Anything).Return([]ReviewRequest{
		{
			TaskID:   "hg-28870",
			Project:  "hg",
			WorkDir:  "/tmp/hg",
			PRNumber: 39,
			Agent:    "gemini",
		},
	}, nil)
	env.OnActivity(da.MarkTaskRunningActivity, mock.Anything, "hg-28870").Return(nil)

	reviewDispatched := 0
	env.OnWorkflow(ReviewWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		reviewDispatched++
		req, ok := args.Get(1).(ReviewRequest)
		if !ok {
			t.Fatalf("expected ReviewRequest argument, got %T", args.Get(1))
		}
		if req.TaskID != "hg-28870" {
			t.Fatalf("TaskID = %q, want hg-28870", req.TaskID)
		}
		if req.PRNumber != 39 {
			t.Fatalf("PRNumber = %d, want 39", req.PRNumber)
		}
	}).Return(nil)

	env.OnWorkflow(AgentWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("unexpected workflow error: %v", err)
	}
	if reviewDispatched != 1 {
		t.Fatalf("expected orphan review dispatch, got %d", reviewDispatched)
	}
}
