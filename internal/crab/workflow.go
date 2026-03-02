package crab

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const defaultSlowStepThreshold = 2 * time.Minute

// DecompositionWorkflow takes a high-level markdown plan and decomposes it
// into whales (epic-level groupings) and morsels (bite-sized executable units).
//
// Pipeline: PARSE → CLARIFY → BLAST SCAN → DECOMPOSE → SCOPE → SIZE → REVIEW → EMIT
func DecompositionWorkflow(ctx workflow.Context, req DecompositionRequest) (*DecompositionResult, error) {
	startTime := workflow.Now(ctx)
	logger := workflow.GetLogger(ctx)
	logger.Info(prefix+" DecompositionWorkflow starting", "PlanID", req.PlanID, "Project", req.Project)

	if req.Tier == "" {
		req.Tier = "fast"
	}

	slowThreshold := defaultSlowStepThreshold
	if req.SlowStepThreshold > 0 {
		slowThreshold = req.SlowStepThreshold
	}

	var stepMetrics []StepMetric
	recordStep := func(name string, stepStart time.Time, status string) {
		dur := workflow.Now(ctx).Sub(stepStart)
		slow := dur >= slowThreshold
		stepMetrics = append(stepMetrics, StepMetric{
			Name:      name,
			DurationS: dur.Seconds(),
			Status:    status,
			Slow:      slow,
		})
		if slow {
			logger.Warn(prefix+" SLOW STEP", "Step", name, "DurationS", dur.Seconds(), "Status", status)
		} else {
			logger.Info(prefix+" Step complete", "Step", name, "DurationS", dur.Seconds(), "Status", status)
		}
	}

	var totalTokens TokenUsage

	shortAO := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	longAO := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	mediumAO := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}

	var a *Activities

	// ===== PHASE 1: PARSE =====
	parseStart := workflow.Now(ctx)
	logger.Info(prefix+" Phase 1: PARSE", "PlanID", req.PlanID)

	parseCtx := workflow.WithActivityOptions(ctx, shortAO)
	var plan ParsedPlan
	if err := workflow.ExecuteActivity(parseCtx, a.ParsePlanActivity, req).Get(ctx, &plan); err != nil {
		recordStep("parse", parseStart, "failed")
		return failResult(req.PlanID, stepMetrics, totalTokens), fmt.Errorf("parse plan: %w", err)
	}
	recordStep("parse", parseStart, "ok")

	logger.Info(prefix+" Plan parsed", "Title", plan.Title, "ScopeItems", len(plan.ScopeItems))

	// ===== PHASE 2: CLARIFY =====
	clarifyStart := workflow.Now(ctx)
	logger.Info(prefix+" Phase 2: CLARIFY", "PlanID", req.PlanID)

	clarifyCtx := workflow.WithActivityOptions(ctx, longAO)
	var clarifications ClarificationResult
	if err := workflow.ExecuteActivity(clarifyCtx, a.ClarifyGapsActivity, req, plan).Get(ctx, &clarifications); err != nil {
		logger.Warn(prefix+" Clarification failed (non-fatal)", "error", err)
		recordStep("clarify", clarifyStart, "failed")
		clarifications = ClarificationResult{}
	} else {
		totalTokens.Add(clarifications.Tokens)
		recordStep("clarify", clarifyStart, "ok")
	}

	// Wait for human clarification if needed
	if clarifications.NeedsHumanInput {
		logger.Info(prefix+" Waiting for human clarification", "Questions", len(clarifications.HumanQuestions))

		clarificationChan := workflow.GetSignalChannel(ctx, "crab-clarification")
		var humanAnswers string

		timer := workflow.NewTimer(ctx, 10*time.Minute)
		sel := workflow.NewSelector(ctx)

		sel.AddReceive(clarificationChan, func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, &humanAnswers)
			logger.Info(prefix + " Human clarification received")
		})

		sel.AddFuture(timer, func(f workflow.Future) {
			humanAnswers = "Human ignored clarification request. Proceeding with best judgement."
			logger.Warn(prefix + " Clarification timed out (10m)")
		})

		sel.Select(ctx)

		clarifications.HumanAnswers = humanAnswers
		clarifications.NeedsHumanInput = false
	}

	// ===== PHASE 2.5: BLAST RADIUS SCAN =====
	scanStart := workflow.Now(ctx)
	logger.Info(prefix+" Phase 2.5: BLAST RADIUS SCAN", "PlanID", req.PlanID)

	scanCtx := workflow.WithActivityOptions(ctx, mediumAO)
	var blastReport BlastRadiusReport
	if err := workflow.ExecuteActivity(scanCtx, a.BlastRadiusScanActivity, req.WorkDir).Get(ctx, &blastReport); err != nil {
		logger.Warn(prefix+" Blast radius scan failed (non-fatal)", "error", err)
		recordStep("blast_scan", scanStart, "failed")
	} else {
		recordStep("blast_scan", scanStart, "ok")
		req.BlastRadius = &blastReport
	}

	// ===== PHASE 3: DECOMPOSE =====
	decomposeStart := workflow.Now(ctx)
	logger.Info(prefix+" Phase 3: DECOMPOSE", "PlanID", req.PlanID)

	decomposeCtx := workflow.WithActivityOptions(ctx, longAO)
	var whales []CandidateWhale
	if err := workflow.ExecuteActivity(decomposeCtx, a.DecomposeActivity, req, plan, clarifications).Get(ctx, &whales); err != nil {
		recordStep("decompose", decomposeStart, "failed")
		return failResult(req.PlanID, stepMetrics, totalTokens), fmt.Errorf("decompose: %w", err)
	}
	recordStep("decompose", decomposeStart, "ok")

	logger.Info(prefix+" Decomposition complete", "Whales", len(whales))

	// ===== PHASE 4: SCOPE =====
	scopeStart := workflow.Now(ctx)
	logger.Info(prefix+" Phase 4: SCOPE", "Whales", len(whales))

	scopeCtx := workflow.WithActivityOptions(ctx, mediumAO)
	var scopedWhales []CandidateWhale
	if err := workflow.ExecuteActivity(scopeCtx, a.ScopeMorselsActivity, req, whales).Get(ctx, &scopedWhales); err != nil {
		logger.Warn(prefix+" Scoping failed (non-fatal), using unscoped", "error", err)
		recordStep("scope", scopeStart, "failed")
		scopedWhales = whales
	} else {
		recordStep("scope", scopeStart, "ok")
	}

	// ===== PHASE 5: SIZE =====
	sizeStart := workflow.Now(ctx)
	logger.Info(prefix+" Phase 5: SIZE", "Whales", len(scopedWhales))

	sizeCtx := workflow.WithActivityOptions(ctx, mediumAO)
	var sizedMorsels []SizedMorsel
	if err := workflow.ExecuteActivity(sizeCtx, a.SizeMorselsActivity, req, scopedWhales).Get(ctx, &sizedMorsels); err != nil {
		recordStep("size", sizeStart, "failed")
		return failResult(req.PlanID, stepMetrics, totalTokens), fmt.Errorf("size: %w", err)
	}
	recordStep("size", sizeStart, "ok")

	logger.Info(prefix+" Sizing complete", "Morsels", len(sizedMorsels))

	// ===== PHASE 6: REVIEW GATE =====
	reviewStart := workflow.Now(ctx)
	decision := "APPROVED"

	if req.RequireHumanReview {
		logger.Info(prefix+" Phase 6: HUMAN REVIEW (10m timeout)",
			"Whales", len(scopedWhales), "Morsels", len(sizedMorsels))

		reviewChan := workflow.GetSignalChannel(ctx, "crab-review")

		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		timer := workflow.NewTimer(timerCtx, 10*time.Minute)

		sel := workflow.NewSelector(ctx)
		sel.AddReceive(reviewChan, func(ch workflow.ReceiveChannel, _ bool) {
			ch.Receive(ctx, &decision)
			cancelTimer()
		})
		sel.AddFuture(timer, func(f workflow.Future) {
			decision = "APPROVED"
			logger.Warn(prefix + " Review timed out (10m) — auto-approving")
		})
		sel.Select(ctx)
	} else {
		logger.Info(prefix+" Phase 6: AUTO-APPROVED",
			"Whales", len(scopedWhales), "Morsels", len(sizedMorsels))
	}

	if decision != "APPROVED" {
		recordStep("review", reviewStart, "rejected")
		logger.Info(prefix+" Plan REJECTED", "Decision", decision)
		return &DecompositionResult{
			Status:      "rejected",
			PlanID:      req.PlanID,
			StepMetrics: stepMetrics,
			TotalTokens: totalTokens,
		}, nil
	}
	recordStep("review", reviewStart, "ok")

	// ===== PHASE 7: EMIT =====
	emitStart := workflow.Now(ctx)
	logger.Info(prefix+" Phase 7: EMIT", "PlanID", req.PlanID)

	emitCtx := workflow.WithActivityOptions(ctx, shortAO)
	var emitResult EmitResult
	if err := workflow.ExecuteActivity(emitCtx, a.EmitMorselsActivity, req, scopedWhales, sizedMorsels).Get(ctx, &emitResult); err != nil {
		recordStep("emit", emitStart, "failed")
		return failResult(req.PlanID, stepMetrics, totalTokens), fmt.Errorf("emit: %w", err)
	}
	recordStep("emit", emitStart, "ok")

	logger.Info(prefix+" DecompositionWorkflow complete",
		"PlanID", req.PlanID,
		"WhalesEmitted", len(emitResult.WhaleIDs),
		"MorselsEmitted", len(emitResult.MorselIDs),
		"FailedCount", emitResult.FailedCount,
		"TotalDuration", workflow.Now(ctx).Sub(startTime).String(),
	)

	return &DecompositionResult{
		Status:         "completed",
		PlanID:         req.PlanID,
		WhalesEmitted:  emitResult.WhaleIDs,
		MorselsEmitted: emitResult.MorselIDs,
		StepMetrics:    stepMetrics,
		TotalTokens:    totalTokens,
	}, nil
}

func failResult(planID string, metrics []StepMetric, tokens TokenUsage) *DecompositionResult {
	return &DecompositionResult{
		Status:      "failed",
		PlanID:      planID,
		StepMetrics: metrics,
		TotalTokens: tokens,
	}
}
