package planning

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
)

// PlanningWorkflow orchestrates the interactive planning ceremony through all phases:
// clarify -> research -> select -> greenlight -> decompose -> approve -> handoff
func PlanningWorkflow(ctx workflow.Context, req PlanningRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("PlanningWorkflow started", "IssueID", req.IssueID, "Project", req.Project)

	// Activity options for planning activities
	activityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	// Initialize workflow state
	state := PlanningState{
		Phase: PhaseClarify,
	}

	var a *Activities
	actCtx := workflow.WithActivityOptions(ctx, activityOpts)

	// === LOAD ORIGINAL ISSUE ===
	logger.Info("Loading original issue")
	var originalIssue *beads.Issue
	if err := workflow.ExecuteActivity(actCtx, a.LoadIssueActivity, req.IssueID).Get(ctx, &originalIssue); err != nil {
		logger.Error("Failed to load issue", "error", err)
		return fmt.Errorf("load issue failed: %w", err)
	}
	state.OriginalIssue = *originalIssue

	// === PHASE 1: CLARIFY ===
	logger.Info("Starting CLARIFY phase")
	state.Phase = PhaseClarify

	var clarifyResult *ClarifyResult
	if err := workflow.ExecuteActivity(actCtx, a.ClarifyActivity, *originalIssue, req.LLMModel).Get(ctx, &clarifyResult); err != nil {
		logger.Error("Clarify phase failed", "error", err)
		return fmt.Errorf("clarify failed: %w", err)
	}
	state.ClarifyResult = clarifyResult
	logger.Info("CLARIFY phase completed", "Complexity", clarifyResult.EstimatedComplexity)

	// Wait for signal to proceed to research
	state.Phase = PhaseResearch
	clarifySignal := workflow.GetSignalChannel(ctx, "planning-signal")
	var signal PlanningSignal
	clarifySignal.Receive(ctx, &signal)
	if signal.Phase != PhaseResearch {
		return fmt.Errorf("expected research signal, got %s", signal.Phase)
	}

	// === PHASE 2: RESEARCH ===
	logger.Info("Starting RESEARCH phase")

	var researchResult *ResearchResult
	if err := workflow.ExecuteActivity(actCtx, a.ResearchActivity, *originalIssue, *clarifyResult, req.WorkDir, req.LLMModel).Get(ctx, &researchResult); err != nil {
		logger.Error("Research phase failed", "error", err)
		return fmt.Errorf("research failed: %w", err)
	}
	state.ResearchResult = researchResult
	logger.Info("RESEARCH phase completed", "Files", len(researchResult.RelevantFiles))

	// Wait for signal to proceed to select
	state.Phase = PhaseSelect
	researchSignal := workflow.GetSignalChannel(ctx, "planning-signal")
	researchSignal.Receive(ctx, &signal)
	if signal.Phase != PhaseSelect {
		return fmt.Errorf("expected select signal, got %s", signal.Phase)
	}

	// === PHASE 3: SELECT ===
	logger.Info("Starting SELECT phase")

	var selectResult *SelectResult
	if err := workflow.ExecuteActivity(actCtx, a.SelectActivity, *originalIssue, *clarifyResult, *researchResult, req.LLMModel).Get(ctx, &selectResult); err != nil {
		logger.Error("Select phase failed", "error", err)
		return fmt.Errorf("select failed: %w", err)
	}
	state.SelectResult = selectResult
	logger.Info("SELECT phase completed", "Approach", selectResult.SelectedApproach)

	// Wait for signal to proceed to greenlight
	state.Phase = PhaseGreenlight
	selectSignal := workflow.GetSignalChannel(ctx, "planning-signal")
	selectSignal.Receive(ctx, &signal)
	if signal.Phase != PhaseGreenlight {
		return fmt.Errorf("expected greenlight signal, got %s", signal.Phase)
	}

	// === PHASE 4: GREENLIGHT ===
	logger.Info("Starting GREENLIGHT phase")
	// Greenlight is typically a human decision - extract from signal
	var greenlightResult GreenlightResult
	if signal.Decision == "approved" {
		greenlightResult.Approved = true
		greenlightResult.Feedback = "Approved for implementation"
	} else {
		greenlightResult.Approved = false
		greenlightResult.Feedback = fmt.Sprintf("Rejected: %s", signal.Decision)
	}
	state.GreenlightResult = &greenlightResult

	if !greenlightResult.Approved {
		logger.Info("GREENLIGHT phase rejected - ending workflow")
		// Record the decision and exit
		if err := workflow.ExecuteActivity(actCtx, a.RecordPlanningDecisionActivity, req, state).Get(ctx, nil); err != nil {
			logger.Error("Failed to record rejection decision", "error", err)
		}
		return fmt.Errorf("planning rejected: %s", greenlightResult.Feedback)
	}

	logger.Info("GREENLIGHT phase completed", "Approved", greenlightResult.Approved)

	// Wait for signal to proceed to decompose
	state.Phase = PhaseDecompose
	greenlightSignal := workflow.GetSignalChannel(ctx, "planning-signal")
	greenlightSignal.Receive(ctx, &signal)
	if signal.Phase != PhaseDecompose {
		return fmt.Errorf("expected decompose signal, got %s", signal.Phase)
	}

	// === PHASE 5: DECOMPOSE ===
	logger.Info("Starting DECOMPOSE phase")

	var decomposeResult *DecomposeResult
	if err := workflow.ExecuteActivity(actCtx, a.DecomposeActivity, *originalIssue, *selectResult, req.LLMModel).Get(ctx, &decomposeResult); err != nil {
		logger.Error("Decompose phase failed", "error", err)
		return fmt.Errorf("decompose failed: %w", err)
	}
	state.DecomposeResult = decomposeResult
	logger.Info("DECOMPOSE phase completed", "Subtasks", len(decomposeResult.Subtasks))

	// Wait for signal to proceed to approve
	state.Phase = PhaseApprove
	decomposeSignal := workflow.GetSignalChannel(ctx, "planning-signal")
	decomposeSignal.Receive(ctx, &signal)
	if signal.Phase != PhaseApprove {
		return fmt.Errorf("expected approve signal, got %s", signal.Phase)
	}

	// === PHASE 6: APPROVE ===
	logger.Info("Starting APPROVE phase")
	// Approval is typically a human decision - extract from signal
	var approvalResult ApprovalResult
	if signal.Decision == "approved" {
		approvalResult.Approved = true
		approvalResult.Feedback = "Subtasks approved for creation"
	} else {
		approvalResult.Approved = false
		approvalResult.Feedback = fmt.Sprintf("Subtasks rejected: %s", signal.Decision)
	}
	state.ApprovalResult = &approvalResult

	if !approvalResult.Approved {
		logger.Info("APPROVE phase rejected - ending workflow")
		// Record the decision and exit
		if err := workflow.ExecuteActivity(actCtx, a.RecordPlanningDecisionActivity, req, state).Get(ctx, nil); err != nil {
			logger.Error("Failed to record approval rejection decision", "error", err)
		}
		return fmt.Errorf("subtasks rejected: %s", approvalResult.Feedback)
	}

	logger.Info("APPROVE phase completed", "Approved", approvalResult.Approved)

	// Wait for signal to proceed to handoff
	state.Phase = PhaseHandoff
	approveSignal := workflow.GetSignalChannel(ctx, "planning-signal")
	approveSignal.Receive(ctx, &signal)
	if signal.Phase != PhaseHandoff {
		return fmt.Errorf("expected handoff signal, got %s", signal.Phase)
	}

	// === PHASE 7: HANDOFF ===
	logger.Info("Starting HANDOFF phase")

	// Create the subtasks in the DAG
	var createdTaskIDs []string
	if err := workflow.ExecuteActivity(actCtx, a.CreateSubtasksActivity,
		decomposeResult.Subtasks, req.Project, req.IssueID).Get(ctx, &createdTaskIDs); err != nil {
		logger.Error("Failed to create subtasks", "error", err)
		return fmt.Errorf("create subtasks failed: %w", err)
	}

	// Prepare handoff result
	handoffResult := HandoffResult{
		CreatedTaskIDs:   createdTaskIDs,
		PlanningDecision: "APPROVED - Subtasks created and ready for execution",
		Summary:         fmt.Sprintf("Planning completed for issue %s. Created %d subtasks: %v",
			req.IssueID, len(createdTaskIDs), createdTaskIDs),
	}
	state.HandoffResult = &handoffResult
	state.Phase = PhaseCompleted

	logger.Info("HANDOFF phase completed", "CreatedTasks", len(createdTaskIDs))

	// Record the final planning decision
	if err := workflow.ExecuteActivity(actCtx, a.RecordPlanningDecisionActivity, req, state).Get(ctx, nil); err != nil {
		logger.Error("Failed to record final decision", "error", err)
		return fmt.Errorf("record decision failed: %w", err)
	}

	logger.Info("PlanningWorkflow completed successfully",
		"IssueID", req.IssueID,
		"CreatedTasks", len(createdTaskIDs),
		"Decision", handoffResult.PlanningDecision)

	return nil
}