package engine

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func TestDispatcherWorkflow_NoCandidates(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return([]DispatchCandidate{}, nil)

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
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return([]DispatchCandidate{
		{
			TaskID:  "task-1",
			Project: "p",
			Prompt:  "do it",
			WorkDir: "/tmp/p",
			Agent:   "claude",
		},
	}, nil)
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
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return([]DispatchCandidate{
		{
			TaskID:  "task-1",
			Project: "p",
			Prompt:  "do it",
			WorkDir: "/tmp/p",
			Agent:   "claude",
			Model:   "claude-sonnet",
			Tier:    "fast",
		},
	}, nil)
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
