package planning

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PlanningWorkflow orchestrates the planning ceremony.
//
// Phases:
//  1. Goal clarification (autonomous)
//  2. Research round 1 (autonomous)
//  3. Goal check (autonomous)
//  4. Push approaches to chat
//  5. Interactive signal loop (human-driven)
//  6. Decompose selected approach (autonomous, gated)
//  7. Approval gate (human)
//  8. Hand to factory
func PlanningWorkflow(ctx workflow.Context, req PlanningRequest, cfg PlanningCeremonyConfig) (*PlanningResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("PlanningWorkflow started",
		"GoalID", req.GoalID, "Project", req.Project, "SessionID", req.SessionID)

	// Apply defaults for unset config values
	if cfg.MaxResearchRounds <= 0 {
		cfg.MaxResearchRounds = 3
	}
	if cfg.SignalTimeout <= 0 {
		cfg.SignalTimeout = 30 * time.Minute
	}
	if cfg.SessionTimeout <= 0 {
		cfg.SessionTimeout = 24 * time.Hour
	}
	if cfg.MaxCycles <= 0 {
		cfg.MaxCycles = 3
	}

	var pa *PlanningActivities

	shortOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	researchOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}

	// Enforce session-level timeout (Fix #9: workflow deadline)
	ctx, sessionCancel := workflow.WithCancel(ctx)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		_ = workflow.NewTimer(gCtx, cfg.SessionTimeout).Get(gCtx, nil)
		sessionCancel()
	})

	// Register signal channels
	selectCh := workflow.GetSignalChannel(ctx, SignalNameSelect)
	digCh := workflow.GetSignalChannel(ctx, SignalNameDig)
	questionCh := workflow.GetSignalChannel(ctx, SignalNameQuestion)
	greenlightCh := workflow.GetSignalChannel(ctx, SignalNameGreenlight)
	approveDecompCh := workflow.GetSignalChannel(ctx, SignalNameApproveDecomp)
	cancelCh := workflow.GetSignalChannel(ctx, SignalNameCancel)

	// Check for cancellation at any point
	cancelled := false
	cancelReason := ""

	drainCancel := func() bool {
		for {
			var sig string
			ok := cancelCh.ReceiveAsync(&sig)
			if !ok {
				return cancelled
			}
			cancelled = true
			cancelReason = sig
		}
	}

	notify := func(message string) {
		if req.RoomID == "" {
			return
		}
		nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
		_ = workflow.ExecuteActivity(nCtx, pa.NotifyChatActivity, req.RoomID, message).Get(ctx, nil)
	}

	// ============================================================
	// PHASE 1: Goal Clarification (autonomous)
	// ============================================================
	logger.Info("Phase: goal clarification")
	var goal ClarifiedGoal
	goalCtx := workflow.WithActivityOptions(ctx, researchOpts)
	if err := workflow.ExecuteActivity(goalCtx, pa.ClarifyGoalActivity, req).Get(ctx, &goal); err != nil {
		logger.Error("Goal clarification failed", "error", err)
		notify(fmt.Sprintf("Goal clarification failed for %s: %s", req.GoalID, err))
		return &PlanningResult{GoalID: req.GoalID, Cancelled: true, CancelReason: "goal_clarification_failed"}, nil
	}
	if drainCancel() {
		return &PlanningResult{GoalID: req.GoalID, Cancelled: true, CancelReason: cancelReason}, nil
	}

	// ============================================================
	// PHASE 2: Research Round 1 (autonomous)
	// ============================================================
	logger.Info("Phase: research round 1")
	var approaches []ResearchedApproach
	researchCtx := workflow.WithActivityOptions(ctx, researchOpts)
	if err := workflow.ExecuteActivity(researchCtx, pa.ResearchApproachesActivity, req, goal).Get(ctx, &approaches); err != nil {
		logger.Error("Research failed", "error", err)
		notify(fmt.Sprintf("Research failed for %s: %s", req.GoalID, err))
		return &PlanningResult{GoalID: req.GoalID, Cancelled: true, CancelReason: "research_failed"}, nil
	}
	if drainCancel() {
		return &PlanningResult{GoalID: req.GoalID, Cancelled: true, CancelReason: cancelReason}, nil
	}

	// ============================================================
	// PHASE 3: Goal Check (autonomous)
	// ============================================================
	logger.Info("Phase: goal check")
	checkCtx := workflow.WithActivityOptions(ctx, researchOpts)
	if err := workflow.ExecuteActivity(checkCtx, pa.GoalCheckActivity, req, goal, approaches).Get(ctx, &approaches); err != nil {
		logger.Warn("Goal check failed, proceeding with unchecked approaches", "error", err)
	}

	// ============================================================
	// PHASE 4: Store approaches as beads + push to chat
	// ============================================================
	logger.Info("Phase: store approaches and push to chat", "count", len(approaches))
	storeCtx := workflow.WithActivityOptions(ctx, shortOpts)
	if err := workflow.ExecuteActivity(storeCtx, pa.StoreApproachesActivity, req, approaches).Get(ctx, &approaches); err != nil {
		logger.Warn("Failed to store approaches as beads", "error", err)
	}

	// Push approaches summary to chat
	summary := formatApproachesSummary(req.SessionID, goal, approaches)
	notify(summary)

	// ============================================================
	// PHASE 5: Interactive Signal Loop (human-driven)
	// ============================================================
	logger.Info("Phase: interactive")
	var selectedApproach *ResearchedApproach
	maxResearchRounds := cfg.MaxResearchRounds
	if maxResearchRounds <= 0 {
		maxResearchRounds = 3
	}
	signalTimeout := cfg.SignalTimeout
	if signalTimeout <= 0 {
		signalTimeout = 30 * time.Minute
	}
	researchRound := 1
	greenlightReceived := false

	for !greenlightReceived {
		if drainCancel() {
			return &PlanningResult{GoalID: req.GoalID, Cancelled: true, CancelReason: cancelReason, Approaches: approaches}, nil
		}

		// Wait for any signal with timeout
		selector := workflow.NewSelector(ctx)
		timedOut := false

		// Timer for signal timeout (configurable reminder)
		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		timerFuture := workflow.NewTimer(timerCtx, signalTimeout)

		selector.AddReceive(selectCh, func(ch workflow.ReceiveChannel, more bool) {
			var value string
			ch.Receive(ctx, &value)
			cancelTimer()
			value = strings.TrimSpace(value)
			for i := range approaches {
				if approaches[i].ID == value || fmt.Sprintf("%d", approaches[i].Rank) == value {
					approaches[i].Status = "selected"
					selectedApproach = &approaches[i]
					logger.Info("Approach selected", "ID", approaches[i].ID, "Title", approaches[i].Title)
					notify(fmt.Sprintf("Selected approach %d: %s\nSend `/plan go` to greenlight decomposition, or `/plan dig %s` for deeper research.",
						approaches[i].Rank, approaches[i].Title, approaches[i].ID))
					break
				}
			}
		})

		selector.AddReceive(digCh, func(ch workflow.ReceiveChannel, more bool) {
			var value string
			ch.Receive(ctx, &value)
			cancelTimer()
			if researchRound >= maxResearchRounds {
				notify("Maximum research rounds reached. Please select an approach or realign.")
				return
			}
			researchRound++

			parts := strings.SplitN(value, "|", 2)
			approachID := strings.TrimSpace(parts[0])
			feedback := ""
			if len(parts) > 1 {
				feedback = strings.TrimSpace(parts[1])
			}

			// Find the approach to dig deeper on
			var target *ResearchedApproach
			for i := range approaches {
				if approaches[i].ID == approachID || fmt.Sprintf("%d", approaches[i].Rank) == approachID {
					target = &approaches[i]
					break
				}
			}
			if target == nil {
				notify(fmt.Sprintf("Approach %q not found.", approachID))
				return
			}

			notify(fmt.Sprintf("Researching approach %d deeper (round %d)...", target.Rank, researchRound))
			deepCtx := workflow.WithActivityOptions(ctx, researchOpts)
			var updated ResearchedApproach
			if err := workflow.ExecuteActivity(deepCtx, pa.DeeperResearchActivity, req, *target, feedback).Get(ctx, &updated); err != nil {
				logger.Warn("Deeper research failed", "error", err)
				notify(fmt.Sprintf("Deeper research failed: %s", err))
				return
			}
			// Update the approach in our list
			for i := range approaches {
				if approaches[i].ID == target.ID {
					approaches[i] = updated
					break
				}
			}
			notify(formatSingleApproach(updated))
		})

		selector.AddReceive(questionCh, func(ch workflow.ReceiveChannel, more bool) {
			var question string
			ch.Receive(ctx, &question)
			cancelTimer()

			qCtx := workflow.WithActivityOptions(ctx, researchOpts)
			var answer string
			if err := workflow.ExecuteActivity(qCtx, pa.AnswerQuestionActivity, req, goal, approaches, question).Get(ctx, &answer); err != nil {
				notify(fmt.Sprintf("Failed to answer: %s", err))
				return
			}
			notify(fmt.Sprintf("Q: %s\n\nA: %s", question, answer))
		})

		selector.AddReceive(greenlightCh, func(ch workflow.ReceiveChannel, more bool) {
			var decision string
			ch.Receive(ctx, &decision)
			cancelTimer()

			decision = strings.ToUpper(strings.TrimSpace(decision))
			if decision == "REALIGN" {
				logger.Info("User requested realignment")
				selectedApproach = nil
				researchRound = 0
				notify("Realigning. Research will restart with fresh context.\nSend `/plan dig <id>` or wait for new approaches.")
				return
			}
			// GO — proceed to decomposition if an approach is selected
			if selectedApproach == nil {
				notify("No approach selected. Use `/plan select <id>` first, then `/plan go`.")
				return
			}
			greenlightReceived = true
		})

		selector.AddReceive(cancelCh, func(ch workflow.ReceiveChannel, more bool) {
			var reason string
			ch.Receive(ctx, &reason)
			cancelTimer()
			cancelled = true
			cancelReason = reason
		})

		selector.AddFuture(timerFuture, func(f workflow.Future) {
			timedOut = true
		})

		selector.Select(ctx)

		if cancelled {
			return &PlanningResult{GoalID: req.GoalID, Cancelled: true, CancelReason: cancelReason, Approaches: approaches}, nil
		}

		if timedOut {
			notify(fmt.Sprintf("Planning session %s is waiting for input. Send `/plan help` for commands.", req.SessionID))
		}
	}

	// ============================================================
	// PHASE 6: Decompose selected approach (autonomous, gated)
	// ============================================================
	logger.Info("Phase: decompose", "Approach", selectedApproach.Title)
	notify(fmt.Sprintf("Decomposing approach: %s...", selectedApproach.Title))

	decompCtx := workflow.WithActivityOptions(ctx, researchOpts)
	var steps []DecompStep
	if err := workflow.ExecuteActivity(decompCtx, pa.DecomposeApproachActivity, req, *selectedApproach).Get(ctx, &steps); err != nil {
		logger.Error("Decomposition failed", "error", err)
		notify(fmt.Sprintf("Decomposition failed: %s. Send `/plan go` to retry or `/plan realign` to restart.", err))
		// TODO: loop back to interactive phase on decomp failure
		return &PlanningResult{GoalID: req.GoalID, Approaches: approaches, SelectedApproach: selectedApproach}, nil
	}

	// Push decomposition to chat for approval
	decompSummary := formatDecompSummary(selectedApproach.Title, steps)
	notify(decompSummary + "\n\nSend `/plan approve` to create subtasks or `/plan realign` to go back.")

	// ============================================================
	// PHASE 7: Decomposition Approval Gate (human)
	// ============================================================
	logger.Info("Phase: approve decomposition")
	approved := false

	for !approved {
		decompSelector := workflow.NewSelector(ctx)

		decompSelector.AddReceive(approveDecompCh, func(ch workflow.ReceiveChannel, more bool) {
			var sig string
			ch.Receive(ctx, &sig)
			approved = true
		})

		decompSelector.AddReceive(greenlightCh, func(ch workflow.ReceiveChannel, more bool) {
			var decision string
			ch.Receive(ctx, &decision)
			decision = strings.ToUpper(strings.TrimSpace(decision))
			if decision == "GO" {
				approved = true
			} else {
				// REALIGN — go back
				notify("Decomposition rejected. Returning to approach selection.")
				// For now, cancel. TODO: loop back to interactive phase.
				cancelled = true
				cancelReason = "decomp_rejected"
			}
		})

		decompSelector.AddReceive(cancelCh, func(ch workflow.ReceiveChannel, more bool) {
			var reason string
			ch.Receive(ctx, &reason)
			cancelled = true
			cancelReason = reason
		})

		decompSelector.Select(ctx)

		if cancelled {
			return &PlanningResult{GoalID: req.GoalID, Cancelled: true, CancelReason: cancelReason, Approaches: approaches, SelectedApproach: selectedApproach}, nil
		}
	}

	// ============================================================
	// PHASE 8: Hand to factory (create subtasks)
	// ============================================================
	logger.Info("Phase: handoff to factory", "Steps", len(steps))
	handoffCtx := workflow.WithActivityOptions(ctx, shortOpts)
	var subtaskIDs []string
	if err := workflow.ExecuteActivity(handoffCtx, pa.CreatePlanSubtasksActivity, req, steps).Get(ctx, &subtaskIDs); err != nil {
		logger.Error("Failed to create subtasks", "error", err)
		notify(fmt.Sprintf("Failed to create subtasks: %s", err))
		return &PlanningResult{GoalID: req.GoalID, Approaches: approaches, SelectedApproach: selectedApproach}, nil
	}

	notify(fmt.Sprintf("Planning complete. %d subtasks created for approach: %s\nSubtask IDs: %s",
		len(subtaskIDs), selectedApproach.Title, strings.Join(subtaskIDs, ", ")))

	return &PlanningResult{
		GoalID:           req.GoalID,
		SelectedApproach: selectedApproach,
		SubtaskIDs:       subtaskIDs,
		Approaches:       approaches,
	}, nil
}

// --- Formatting helpers ---

func formatApproachesSummary(sessionID string, goal ClarifiedGoal, approaches []ResearchedApproach) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Planning ready for session %s\n\n", sessionID)
	fmt.Fprintf(&b, "Goal: %s\n", goal.Intent)
	if goal.Why != "" {
		fmt.Fprintf(&b, "Why: %s\n", goal.Why)
	}
	fmt.Fprintf(&b, "\n%d approaches researched:\n\n", len(approaches))
	for _, a := range approaches {
		fmt.Fprintf(&b, "%d. %s (confidence: %.0f%%)\n   %s\n   Tradeoffs: %s\n\n",
			a.Rank, a.Title, a.Confidence*100, a.Description, a.Tradeoffs)
	}
	b.WriteString("Commands:\n")
	b.WriteString("  /plan select <N>  — choose an approach\n")
	b.WriteString("  /plan dig <N>     — research an approach deeper\n")
	b.WriteString("  /plan answer <text> — ask a question\n")
	b.WriteString("  /plan go          — greenlight selected approach\n")
	return b.String()
}

func formatSingleApproach(a ResearchedApproach) string {
	return fmt.Sprintf("Approach %d: %s (confidence: %.0f%%)\n%s\nTradeoffs: %s",
		a.Rank, a.Title, a.Confidence*100, a.Description, a.Tradeoffs)
}

func formatDecompSummary(title string, steps []DecompStep) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Decomposition for: %s\n\n", title)
	for i, s := range steps {
		fmt.Fprintf(&b, "%d. %s (~%dm)\n   %s\n   Acceptance: %s\n\n",
			i+1, s.Title, s.Estimate, s.Description, s.Acceptance)
	}
	return b.String()
}
