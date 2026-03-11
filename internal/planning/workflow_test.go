package planning

import (
	"testing"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

var defaultCfg = PlanningCeremonyConfig{
	MaxResearchRounds: 3,
	SignalTimeout:     time.Minute,
	SessionTimeout:    10 * time.Minute,
	MaxCycles:         3,
}

func baseRequest() PlanningRequest {
	return PlanningRequest{
		GoalID:    "goal-1",
		Project:   "testproject",
		WorkDir:   "/tmp/work",
		Agent:     "claude",
		RoomID:    "!room:test",
		Source:    "test",
		SessionID: "planning-test-1",
	}
}

func defaultPlanSpec() types.PlanSpec {
	return types.PlanSpec{
		ProblemStatement:   "Planning is opaque",
		DesiredOutcome:     "A reviewable plan",
		ExpectedPROutcome:  "Add plan storage and API output",
		Summary:            "Persist and surface a validated plan before execution.",
		ChosenApproach:     types.PlanningApproach{Title: "Persist PlanSpec"},
		NonGoals:           []string{"Rewriting the entire planner"},
		Risks:              []string{"Schema drift"},
		ValidationStrategy: []string{"Run focused workflow and API tests"},
		Steps:              []types.DecompStep{{Title: "Persist plan", Description: "Store snapshot", Acceptance: "Readable via API", Estimate: 10}},
	}
}

func mockPlanningPersistence(env *testsuite.TestWorkflowEnvironment, pa *PlanningActivities, includePlanSpec bool) {
	env.OnActivity(pa.StorePlanningSnapshotActivity, mock.Anything, mock.Anything).Return(nil)
	if includePlanSpec {
		spec := defaultPlanSpec()
		env.OnActivity(pa.BuildPlanSpecActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&spec, nil)
	}
}

// TestPlanningWorkflow_HappyPath exercises the full ceremony:
// clarify → research → goal check → present → select → greenlight → decompose → approve → handoff.
func TestPlanningWorkflow_HappyPath(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var pa *PlanningActivities

	goal := ClarifiedGoal{
		Intent:      "Build a caching layer",
		Constraints: []string{"Must use Redis"},
		Why:         "Reduce latency",
		Raw:         "Build caching",
	}
	approaches := []ResearchedApproach{
		{ID: "approach-1", Title: "Redis Sidecar", Description: "Run Redis alongside", Tradeoffs: "Simple but coupled", Confidence: 0.85, Rank: 1, Status: "exploring"},
		{ID: "approach-2", Title: "In-Memory LRU", Description: "Use sync.Map with eviction", Tradeoffs: "No external deps but volatile", Confidence: 0.7, Rank: 2, Status: "exploring"},
	}
	steps := []types.DecompStep{
		{Title: "Add Redis client", Description: "Wire up go-redis", Acceptance: "Client connects", Estimate: 30},
		{Title: "Implement cache layer", Description: "Add Get/Set with TTL", Acceptance: "Unit tests pass", Estimate: 60},
	}

	env.OnActivity(pa.ClarifyGoalActivity, mock.Anything, mock.Anything).Return(&goal, nil)
	env.OnActivity(pa.ResearchApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.GoalCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.StoreApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.NotifyChatActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(pa.DecomposeApproachActivity, mock.Anything, mock.Anything, mock.Anything).Return(steps, nil)
	mockPlanningPersistence(env, pa, true)
	env.OnActivity(pa.RecordPlanningDecisionActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("dec-1", nil)
	env.OnActivity(pa.RecordPlanningDecisionActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("dec-1", nil)
	env.OnActivity(pa.CreatePlanSubtasksActivity, mock.Anything, mock.Anything, mock.Anything).Return([]string{"sub-1", "sub-2"}, nil)

	// After autonomous phases complete and approaches are presented,
	// send signals: select approach 1, then greenlight, then approve decomposition.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameSelect, "1")
	}, time.Second*1)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "GO")
	}, time.Second*2)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameApproveDecomp, "APPROVED")
	}, time.Second*3)

	env.ExecuteWorkflow(PlanningWorkflow, baseRequest(), defaultCfg)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PlanningResult
	require.NoError(t, env.GetWorkflowResult(&result))

	require.Equal(t, "goal-1", result.GoalID)
	require.NotNil(t, result.SelectedApproach)
	require.Equal(t, "Redis Sidecar", result.SelectedApproach.Title)
	require.Len(t, result.SubtaskIDs, 2)
	require.Equal(t, []string{"sub-1", "sub-2"}, result.SubtaskIDs)
	require.Equal(t, "dec-1", result.DecisionID)
	require.NotNil(t, result.PlanSpec)
	require.False(t, result.Cancelled)
}

// TestPlanningWorkflow_CancelDuringResearch verifies cancellation during autonomous phases.
func TestPlanningWorkflow_CancelDuringResearch(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var pa *PlanningActivities

	goal := ClarifiedGoal{Intent: "Test cancel", Raw: "cancel test"}
	approaches := []ResearchedApproach{
		{ID: "a-1", Title: "Approach A", Confidence: 0.8, Rank: 1, Status: "exploring"},
	}

	env.OnActivity(pa.ClarifyGoalActivity, mock.Anything, mock.Anything).Return(&goal, nil)
	env.OnActivity(pa.ResearchApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.GoalCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.StoreApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.NotifyChatActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockPlanningPersistence(env, pa, false)

	// Cancel before interactive phase starts
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameCancel, "user_cancelled")
	}, time.Second*1)

	env.ExecuteWorkflow(PlanningWorkflow, baseRequest(), defaultCfg)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PlanningResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.True(t, result.Cancelled)
	require.Equal(t, "user_cancelled", result.CancelReason)
}

// TestPlanningWorkflow_GoalClarificationFailure verifies graceful exit on phase 1 failure.
func TestPlanningWorkflow_GoalClarificationFailure(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var pa *PlanningActivities

	env.OnActivity(pa.ClarifyGoalActivity, mock.Anything, mock.Anything).Return((*ClarifiedGoal)(nil), errMock("goal clarification exploded"))
	env.OnActivity(pa.NotifyChatActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockPlanningPersistence(env, pa, false)

	env.ExecuteWorkflow(PlanningWorkflow, baseRequest(), defaultCfg)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PlanningResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.True(t, result.Cancelled)
	require.Equal(t, "goal_clarification_failed", result.CancelReason)
}

// TestPlanningWorkflow_DecompRejected_ReturnsToSelection verifies the cycle-back
// when the human rejects a decomposition.
func TestPlanningWorkflow_DecompRejected_ReturnsToSelection(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var pa *PlanningActivities

	goal := ClarifiedGoal{Intent: "Multi-cycle test", Raw: "test"}
	approaches := []ResearchedApproach{
		{ID: "a-1", Title: "First", Confidence: 0.9, Rank: 1, Status: "exploring"},
		{ID: "a-2", Title: "Second", Confidence: 0.7, Rank: 2, Status: "exploring"},
	}
	steps := []types.DecompStep{
		{Title: "Step 1", Description: "Do it", Acceptance: "Done", Estimate: 15},
	}

	env.OnActivity(pa.ClarifyGoalActivity, mock.Anything, mock.Anything).Return(&goal, nil)
	env.OnActivity(pa.ResearchApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.GoalCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.StoreApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.NotifyChatActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(pa.DecomposeApproachActivity, mock.Anything, mock.Anything, mock.Anything).Return(steps, nil)
	mockPlanningPersistence(env, pa, true)
	env.OnActivity(pa.RecordPlanningDecisionActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("dec-1", nil)
	env.OnActivity(pa.CreatePlanSubtasksActivity, mock.Anything, mock.Anything, mock.Anything).Return([]string{"sub-1"}, nil)

	// Cycle 1: select approach 1 → greenlight → reject decomposition (REALIGN via greenlight)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameSelect, "1")
	}, time.Second*1)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "GO")
	}, time.Second*2)

	// Reject by sending REALIGN on the greenlight channel during decomp approval
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "REALIGN")
	}, time.Second*3)

	// Cycle 2: select approach 2 → greenlight → approve
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameSelect, "2")
	}, time.Second*4)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "GO")
	}, time.Second*5)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameApproveDecomp, "APPROVED")
	}, time.Second*6)

	env.ExecuteWorkflow(PlanningWorkflow, baseRequest(), defaultCfg)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PlanningResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.False(t, result.Cancelled)
	require.NotNil(t, result.SelectedApproach)
	require.Equal(t, "Second", result.SelectedApproach.Title)
	require.Len(t, result.SubtaskIDs, 1)
}

// TestPlanningWorkflow_DeeperResearch verifies the dig signal triggers deeper research.
func TestPlanningWorkflow_DeeperResearch(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var pa *PlanningActivities

	goal := ClarifiedGoal{Intent: "Dig test", Raw: "test"}
	approaches := []ResearchedApproach{
		{ID: "a-1", Title: "Alpha", Confidence: 0.6, Rank: 1, Status: "exploring"},
	}
	updatedApproach := ResearchedApproach{
		ID: "a-1", Title: "Alpha (deeper)", Confidence: 0.85, Rank: 1, Status: "exploring",
		Description: "Now with deeper understanding",
	}
	steps := []types.DecompStep{
		{Title: "Step 1", Description: "Do it", Acceptance: "Done", Estimate: 20},
	}

	env.OnActivity(pa.ClarifyGoalActivity, mock.Anything, mock.Anything).Return(&goal, nil)
	env.OnActivity(pa.ResearchApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.GoalCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.StoreApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.DeeperResearchActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&updatedApproach, nil)
	env.OnActivity(pa.NotifyChatActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(pa.DecomposeApproachActivity, mock.Anything, mock.Anything, mock.Anything).Return(steps, nil)
	mockPlanningPersistence(env, pa, true)
	env.OnActivity(pa.RecordPlanningDecisionActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("dec-1", nil)
	env.OnActivity(pa.CreatePlanSubtasksActivity, mock.Anything, mock.Anything, mock.Anything).Return([]string{"sub-1"}, nil)

	// Dig into approach 1, then select, greenlight, approve
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameDig, "1|I want to know about performance")
	}, time.Second*1)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameSelect, "1")
	}, time.Second*2)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "GO")
	}, time.Second*3)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameApproveDecomp, "APPROVED")
	}, time.Second*4)

	env.ExecuteWorkflow(PlanningWorkflow, baseRequest(), defaultCfg)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PlanningResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.False(t, result.Cancelled)
	require.NotNil(t, result.SelectedApproach)
	require.Len(t, result.SubtaskIDs, 1)
}

// TestPlanningWorkflow_QuestionAnswer verifies the Q&A signal works during interactive phase.
func TestPlanningWorkflow_QuestionAnswer(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var pa *PlanningActivities

	goal := ClarifiedGoal{Intent: "Q&A test", Raw: "test"}
	approaches := []ResearchedApproach{
		{ID: "a-1", Title: "Only Option", Confidence: 0.75, Rank: 1, Status: "exploring"},
	}
	steps := []types.DecompStep{
		{Title: "Implement", Description: "Build it", Acceptance: "Works", Estimate: 45},
	}

	env.OnActivity(pa.ClarifyGoalActivity, mock.Anything, mock.Anything).Return(&goal, nil)
	env.OnActivity(pa.ResearchApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.GoalCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.StoreApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.AnswerQuestionActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("Redis is better for this use case because...", nil)
	env.OnActivity(pa.NotifyChatActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(pa.DecomposeApproachActivity, mock.Anything, mock.Anything, mock.Anything).Return(steps, nil)
	mockPlanningPersistence(env, pa, true)
	env.OnActivity(pa.RecordPlanningDecisionActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("dec-1", nil)
	env.OnActivity(pa.CreatePlanSubtasksActivity, mock.Anything, mock.Anything, mock.Anything).Return([]string{"sub-1"}, nil)

	// Ask a question, then select, greenlight, approve
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameQuestion, "Should I use Redis or Memcached?")
	}, time.Second*1)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameSelect, "1")
	}, time.Second*2)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "GO")
	}, time.Second*3)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameApproveDecomp, "APPROVED")
	}, time.Second*4)

	env.ExecuteWorkflow(PlanningWorkflow, baseRequest(), defaultCfg)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PlanningResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.False(t, result.Cancelled)
	require.Len(t, result.SubtaskIDs, 1)
}

// TestPlanningWorkflow_NoRoomID verifies notifications are skipped when no room is set.
func TestPlanningWorkflow_NoRoomID(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var pa *PlanningActivities

	goal := ClarifiedGoal{Intent: "No room test", Raw: "test"}
	approaches := []ResearchedApproach{
		{ID: "a-1", Title: "Solo", Confidence: 0.9, Rank: 1, Status: "exploring"},
	}
	steps := []types.DecompStep{
		{Title: "Do it", Description: "Just do it", Acceptance: "Tests pass", Estimate: 10},
	}

	req := baseRequest()
	req.RoomID = "" // No Matrix room

	env.OnActivity(pa.ClarifyGoalActivity, mock.Anything, mock.Anything).Return(&goal, nil)
	env.OnActivity(pa.ResearchApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.GoalCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.StoreApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.DecomposeApproachActivity, mock.Anything, mock.Anything, mock.Anything).Return(steps, nil)
	mockPlanningPersistence(env, pa, true)
	env.OnActivity(pa.RecordPlanningDecisionActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("dec-1", nil)
	env.OnActivity(pa.CreatePlanSubtasksActivity, mock.Anything, mock.Anything, mock.Anything).Return([]string{"sub-1"}, nil)
	// NotifyChatActivity should NOT be called since RoomID is empty

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameSelect, "1")
	}, time.Second*1)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "GO")
	}, time.Second*2)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameApproveDecomp, "APPROVED")
	}, time.Second*3)

	env.ExecuteWorkflow(PlanningWorkflow, req, defaultCfg)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PlanningResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.False(t, result.Cancelled)
	require.Len(t, result.SubtaskIDs, 1)
}

// TestPlanningWorkflow_Realign verifies the realign signal clears selection and resets research.
func TestPlanningWorkflow_Realign(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var pa *PlanningActivities

	goal := ClarifiedGoal{Intent: "Realign test", Raw: "test"}
	approaches := []ResearchedApproach{
		{ID: "a-1", Title: "Option A", Confidence: 0.8, Rank: 1, Status: "exploring"},
		{ID: "a-2", Title: "Option B", Confidence: 0.6, Rank: 2, Status: "exploring"},
	}
	steps := []types.DecompStep{
		{Title: "Build", Description: "Build it", Acceptance: "Works", Estimate: 30},
	}

	env.OnActivity(pa.ClarifyGoalActivity, mock.Anything, mock.Anything).Return(&goal, nil)
	env.OnActivity(pa.ResearchApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.GoalCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.StoreApproachesActivity, mock.Anything, mock.Anything, mock.Anything).Return(approaches, nil)
	env.OnActivity(pa.NotifyChatActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(pa.DecomposeApproachActivity, mock.Anything, mock.Anything, mock.Anything).Return(steps, nil)
	mockPlanningPersistence(env, pa, true)
	env.OnActivity(pa.RecordPlanningDecisionActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("dec-1", nil)
	env.OnActivity(pa.CreatePlanSubtasksActivity, mock.Anything, mock.Anything, mock.Anything).Return([]string{"sub-1"}, nil)

	// Select approach 1, then realign, then select approach 2, greenlight, approve
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameSelect, "1")
	}, time.Second*1)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "REALIGN")
	}, time.Second*2)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameSelect, "2")
	}, time.Second*3)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameGreenlight, "GO")
	}, time.Second*4)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameApproveDecomp, "APPROVED")
	}, time.Second*5)

	env.ExecuteWorkflow(PlanningWorkflow, baseRequest(), defaultCfg)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PlanningResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.False(t, result.Cancelled)
	require.NotNil(t, result.SelectedApproach)
	require.Equal(t, "Option B", result.SelectedApproach.Title)
}

// errMock is a convenience for creating mock errors.
type errMock string

func (e errMock) Error() string { return string(e) }
