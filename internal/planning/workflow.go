package planning

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
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
	c := newCeremony(ctx, req, cfg)

	if err := c.runAutonomousPhases(ctx); err != nil {
		if pe, ok := err.(phaseError); ok {
			return pe.result, nil
		}
		return nil, err
	}

	c.storeAndPresent(ctx)

	if err := c.runInteractiveCycles(ctx); err != nil {
		if pe, ok := err.(phaseError); ok {
			return pe.result, nil
		}
		return nil, err
	}

	return c.handoff(ctx)
}

// ceremony holds shared state for the planning workflow phases.
type ceremony struct {
	req PlanningRequest
	cfg PlanningCeremonyConfig

	// nil — intentional. Temporal uses method references only to resolve activity type names.
	pa *PlanningActivities

	shortOpts    workflow.ActivityOptions
	researchOpts workflow.ActivityOptions
	notifyOpts   workflow.ActivityOptions

	// Signal channels
	selectCh      workflow.ReceiveChannel
	digCh         workflow.ReceiveChannel
	questionCh    workflow.ReceiveChannel
	greenlightCh  workflow.ReceiveChannel
	approveDecompCh workflow.ReceiveChannel
	cancelCh      workflow.ReceiveChannel

	// Shared mutable state
	cancelled        bool
	cancelReason     string
	goal             ClarifiedGoal
	approaches       []ResearchedApproach
	selectedApproach *ResearchedApproach
	steps            []types.DecompStep
	researchRound    int

	// Disconnected context for notifications after session timeout.
	notifyBaseCtx workflow.Context
}

// phaseError wraps an early-exit result from a phase.
type phaseError struct {
	result *PlanningResult
}

func (e phaseError) Error() string { return "phase exit" }

func newCeremony(ctx workflow.Context, req PlanningRequest, cfg PlanningCeremonyConfig) *ceremony {
	logger := workflow.GetLogger(ctx)
	logger.Info("PlanningWorkflow started",
		"GoalID", req.GoalID, "Project", req.Project, "SessionID", req.SessionID)

	// Apply defaults for unset config values (safety nets for Temporal replay).
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

	c := &ceremony{
		req:           req,
		cfg:           cfg,
		researchRound: 1,
		shortOpts: workflow.ActivityOptions{
			StartToCloseTimeout: 2 * time.Minute,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		},
		researchOpts: workflow.ActivityOptions{
			StartToCloseTimeout: 15 * time.Minute,
			RetryPolicy: &temporal.RetryPolicy{
				MaximumAttempts:    5,
				InitialInterval:    30 * time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    5 * time.Minute,
			},
		},
		notifyOpts: workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
		},
	}

	// Enforce session-level timeout.
	ctx, sessionCancel := workflow.WithCancel(ctx)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		_ = workflow.NewTimer(gCtx, cfg.SessionTimeout).Get(gCtx, nil)
		c.cancelled = true
		c.cancelReason = "session_timeout"
		sessionCancel()
	})

	// Register signal channels.
	c.selectCh = workflow.GetSignalChannel(ctx, SignalNameSelect)
	c.digCh = workflow.GetSignalChannel(ctx, SignalNameDig)
	c.questionCh = workflow.GetSignalChannel(ctx, SignalNameQuestion)
	c.greenlightCh = workflow.GetSignalChannel(ctx, SignalNameGreenlight)
	c.approveDecompCh = workflow.GetSignalChannel(ctx, SignalNameApproveDecomp)
	c.cancelCh = workflow.GetSignalChannel(ctx, SignalNameCancel)

	c.notifyBaseCtx, _ = workflow.NewDisconnectedContext(ctx)

	return c
}

// notify sends a chat message via a disconnected context.
func (c *ceremony) notify(message string) {
	if c.req.RoomID == "" {
		return
	}
	nCtx := workflow.WithActivityOptions(c.notifyBaseCtx, c.notifyOpts)
	_ = workflow.ExecuteActivity(nCtx, c.pa.NotifyChatActivity, c.req.RoomID, message).Get(nCtx, nil)
}

// drainCancel checks for any pending cancel signals.
func (c *ceremony) drainCancel(ctx workflow.Context) bool {
	for {
		var sig string
		ok := c.cancelCh.ReceiveAsync(&sig)
		if !ok {
			break
		}
		c.cancelled = true
		c.cancelReason = sig
	}
	if ctx.Err() != nil && !c.cancelled {
		c.cancelled = true
		c.cancelReason = "session_timeout"
	}
	return c.cancelled
}

// cancelledResult returns a PlanningResult for cancellation.
func (c *ceremony) cancelledResult() *PlanningResult {
	return &PlanningResult{
		GoalID:           c.req.GoalID,
		Cancelled:        true,
		CancelReason:     c.cancelReason,
		Approaches:       c.approaches,
		SelectedApproach: c.selectedApproach,
	}
}

// runAutonomousPhases executes phases 1-3 (goal clarification, research, goal check).
func (c *ceremony) runAutonomousPhases(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	// Phase 1: Goal Clarification
	logger.Info("Phase: goal clarification")
	goalCtx := workflow.WithActivityOptions(ctx, c.researchOpts)
	if err := workflow.ExecuteActivity(goalCtx, c.pa.ClarifyGoalActivity, c.req).Get(ctx, &c.goal); err != nil {
		logger.Error("Goal clarification failed", "error", err)
		c.notify(fmt.Sprintf("Goal clarification failed for %s: %s", c.req.GoalID, err))
		return phaseError{&PlanningResult{GoalID: c.req.GoalID, Cancelled: true, CancelReason: "goal_clarification_failed"}}
	}
	if c.drainCancel(ctx) {
		return phaseError{c.cancelledResult()}
	}

	// Phase 2: Research Round 1
	logger.Info("Phase: research round 1")
	researchCtx := workflow.WithActivityOptions(ctx, c.researchOpts)
	if err := workflow.ExecuteActivity(researchCtx, c.pa.ResearchApproachesActivity, c.req, c.goal).Get(ctx, &c.approaches); err != nil {
		logger.Error("Research failed", "error", err)
		c.notify(fmt.Sprintf("Research failed for %s: %s", c.req.GoalID, err))
		return phaseError{&PlanningResult{GoalID: c.req.GoalID, Cancelled: true, CancelReason: "research_failed"}}
	}
	if c.drainCancel(ctx) {
		return phaseError{c.cancelledResult()}
	}

	// Phase 3: Goal Check
	logger.Info("Phase: goal check")
	checkCtx := workflow.WithActivityOptions(ctx, c.researchOpts)
	if err := workflow.ExecuteActivity(checkCtx, c.pa.GoalCheckActivity, c.req, c.goal, c.approaches).Get(ctx, &c.approaches); err != nil {
		logger.Warn("Goal check failed, proceeding with unchecked approaches", "error", err)
	}
	if c.drainCancel(ctx) {
		return phaseError{c.cancelledResult()}
	}

	return nil
}

// storeAndPresent stores approaches as beads and pushes them to chat (phase 4).
func (c *ceremony) storeAndPresent(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Phase: store approaches and push to chat", "count", len(c.approaches))

	storeCtx := workflow.WithActivityOptions(ctx, c.shortOpts)
	if err := workflow.ExecuteActivity(storeCtx, c.pa.StoreApproachesActivity, c.req, c.approaches).Get(ctx, &c.approaches); err != nil {
		logger.Warn("Failed to store approaches as beads", "error", err)
	}

	summary := formatApproachesSummary(c.req.SessionID, c.goal, c.approaches)
	c.notify(summary)

	// Drain any signals that arrived during autonomous phases 1-4.
	drainChannel := func(ch workflow.ReceiveChannel) {
		for {
			var discard string
			if !ch.ReceiveAsync(&discard) {
				return
			}
			logger.Warn("Drained premature signal", "value", discard)
		}
	}
	drainChannel(c.selectCh)
	drainChannel(c.digCh)
	drainChannel(c.questionCh)
	drainChannel(c.greenlightCh)
	drainChannel(c.approveDecompCh)
}

// runInteractiveCycles runs phases 5-7 in a loop until approval or max cycles.
func (c *ceremony) runInteractiveCycles(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	handoffReady := false

	for cycle := 0; cycle < c.cfg.MaxCycles && !handoffReady; cycle++ {
		if cycle > 0 {
			logger.Info("Returning to approach selection", "Cycle", cycle+1)
		}

		// Phase 5: Interactive signal loop
		if err := c.interactiveSelect(ctx, cycle); err != nil {
			return err
		}

		// Phase 6: Decompose
		if err := c.decompose(ctx); err != nil {
			if _, ok := err.(phaseError); ok {
				return err
			}
			// Decomposition failed — loop back to phase 5
			continue
		}

		// Phase 7: Approval gate
		approved, err := c.approveDecomp(ctx)
		if err != nil {
			return err
		}
		if approved {
			handoffReady = true
		} else {
			c.notify("Decomposition rejected. Returning to approach selection.")
			logger.Info("Decomposition rejected, returning to interactive phase")
		}
	}

	if !handoffReady {
		c.notify("Maximum ceremony cycles reached. Planning session ending.")
		return phaseError{&PlanningResult{GoalID: c.req.GoalID, Approaches: c.approaches, SelectedApproach: c.selectedApproach}}
	}
	return nil
}

// interactiveSelect runs phase 5: the human-driven signal loop for approach selection.
func (c *ceremony) interactiveSelect(ctx workflow.Context, cycle int) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Phase: interactive", "Cycle", cycle+1)
	c.selectedApproach = nil
	greenlightReceived := false

	for !greenlightReceived {
		if c.drainCancel(ctx) {
			return phaseError{c.cancelledResult()}
		}

		selector := workflow.NewSelector(ctx)
		timedOut := false

		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		timerFuture := workflow.NewTimer(timerCtx, c.cfg.SignalTimeout)

		selector.AddReceive(c.selectCh, func(ch workflow.ReceiveChannel, more bool) {
			var value string
			ch.Receive(ctx, &value)
			cancelTimer()
			value = strings.TrimSpace(value)
			found := false
			for i := range c.approaches {
				if c.approaches[i].ID == value || (c.approaches[i].Rank > 0 && fmt.Sprintf("%d", c.approaches[i].Rank) == value) {
					c.approaches[i].Status = "selected"
					copy := c.approaches[i]
					c.selectedApproach = &copy
					found = true
					logger.Info("Approach selected", "ID", c.approaches[i].ID, "Title", c.approaches[i].Title)
					c.notify(fmt.Sprintf("Selected approach %d: %s\nSend `/plan go` to greenlight decomposition, or `/plan dig %s` for deeper research.",
						c.approaches[i].Rank, c.approaches[i].Title, c.approaches[i].ID))
					break
				}
			}
			if !found {
				c.notify(fmt.Sprintf("Approach %q not found. Available: 1-%d", value, len(c.approaches)))
			}
		})

		selector.AddReceive(c.digCh, func(ch workflow.ReceiveChannel, more bool) {
			var value string
			ch.Receive(ctx, &value)
			cancelTimer()
			if c.researchRound >= c.cfg.MaxResearchRounds {
				c.notify("Maximum research rounds reached. Please select an approach or realign.")
				return
			}
			c.researchRound++

			parts := strings.SplitN(value, "|", 2)
			approachID := strings.TrimSpace(parts[0])
			feedback := ""
			if len(parts) > 1 {
				feedback = strings.TrimSpace(parts[1])
			}

			var target *ResearchedApproach
			for i := range c.approaches {
				if c.approaches[i].ID == approachID || fmt.Sprintf("%d", c.approaches[i].Rank) == approachID {
					target = &c.approaches[i]
					break
				}
			}
			if target == nil {
				c.notify(fmt.Sprintf("Approach %q not found.", approachID))
				return
			}

			c.notify(fmt.Sprintf("Researching approach %d deeper (round %d)...", target.Rank, c.researchRound))
			deepCtx := workflow.WithActivityOptions(ctx, c.researchOpts)
			var updated ResearchedApproach
			if err := workflow.ExecuteActivity(deepCtx, c.pa.DeeperResearchActivity, c.req, *target, feedback).Get(ctx, &updated); err != nil {
				logger.Warn("Deeper research failed", "error", err)
				c.notify(fmt.Sprintf("Deeper research failed: %s", err))
				return
			}
			for i := range c.approaches {
				if c.approaches[i].ID == target.ID {
					wasSelected := c.selectedApproach != nil && c.selectedApproach.ID == c.approaches[i].ID
					c.approaches[i] = updated
					if wasSelected {
						c.approaches[i].Status = "selected"
						copy := c.approaches[i]
						c.selectedApproach = &copy
					}
					break
				}
			}
			c.notify(formatSingleApproach(updated))
		})

		selector.AddReceive(c.questionCh, func(ch workflow.ReceiveChannel, more bool) {
			var question string
			ch.Receive(ctx, &question)
			cancelTimer()

			qCtx := workflow.WithActivityOptions(ctx, c.researchOpts)
			var answer string
			if err := workflow.ExecuteActivity(qCtx, c.pa.AnswerQuestionActivity, c.req, c.goal, c.approaches, question).Get(ctx, &answer); err != nil {
				c.notify(fmt.Sprintf("Failed to answer: %s", err))
				return
			}
			c.notify(fmt.Sprintf("Q: %s\n\nA: %s", question, answer))
		})

		selector.AddReceive(c.greenlightCh, func(ch workflow.ReceiveChannel, more bool) {
			var decision string
			ch.Receive(ctx, &decision)
			cancelTimer()

			decision = strings.ToUpper(strings.TrimSpace(decision))
			if decision == "REALIGN" {
				logger.Info("User requested realignment")
				c.selectedApproach = nil
				c.researchRound = 0
				c.notify("Realigning. Selection cleared — use `/plan dig <id>` to research further, then `/plan select <id>` and `/plan go`.")
				return
			}
			if c.selectedApproach == nil {
				c.notify("No approach selected. Use `/plan select <id>` first, then `/plan go`.")
				return
			}
			greenlightReceived = true
		})

		selector.AddReceive(c.cancelCh, func(ch workflow.ReceiveChannel, more bool) {
			var reason string
			ch.Receive(ctx, &reason)
			cancelTimer()
			c.cancelled = true
			c.cancelReason = reason
		})

		selector.AddFuture(timerFuture, func(f workflow.Future) {
			cancelTimer()
			timedOut = true
		})

		selector.Select(ctx)

		if c.cancelled {
			return phaseError{c.cancelledResult()}
		}

		if timedOut {
			c.notify(fmt.Sprintf("Planning session %s is waiting for input. Send `/plan help` for commands.", c.req.SessionID))
		}
	}

	return nil
}

// decompose runs phase 6: decompose the selected approach into subtasks.
// Returns nil on success, phaseError on cancellation, or a generic error to signal retry.
func (c *ceremony) decompose(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	if c.selectedApproach == nil {
		logger.Error("Reached decompose phase with nil selectedApproach")
		c.notify("Internal error: no approach selected. Returning to selection.")
		return fmt.Errorf("no approach selected")
	}

	logger.Info("Phase: decompose", "Approach", c.selectedApproach.Title)
	c.notify(fmt.Sprintf("Decomposing approach: %s...", c.selectedApproach.Title))

	decompCtx := workflow.WithActivityOptions(ctx, c.researchOpts)
	if err := workflow.ExecuteActivity(decompCtx, c.pa.DecomposeApproachActivity, c.req, *c.selectedApproach).Get(ctx, &c.steps); err != nil {
		logger.Error("Decomposition failed", "error", err)
		c.notify(fmt.Sprintf("Decomposition failed: %s\nReturning to approach selection. Use `/plan go` to retry or `/plan select <id>` to pick a different approach.", err))
		return fmt.Errorf("decomposition failed")
	}

	decompSummary := formatDecompSummary(c.selectedApproach.Title, c.steps)
	c.notify(decompSummary + "\n\nSend `/plan approve` to create subtasks or `/plan realign` to go back.")
	return nil
}

// approveDecomp runs phase 7: wait for human approval of the decomposition.
func (c *ceremony) approveDecomp(ctx workflow.Context) (bool, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Phase: approve decomposition")
	approved := false
	rejected := false

	for !approved && !rejected {
		decompSelector := workflow.NewSelector(ctx)

		decompTimerCtx, decompTimerCancel := workflow.WithCancel(ctx)
		decompTimerFuture := workflow.NewTimer(decompTimerCtx, c.cfg.SignalTimeout)

		decompSelector.AddReceive(c.approveDecompCh, func(ch workflow.ReceiveChannel, more bool) {
			var sig string
			ch.Receive(ctx, &sig)
			decompTimerCancel()
			approved = true
		})

		decompSelector.AddReceive(c.greenlightCh, func(ch workflow.ReceiveChannel, more bool) {
			var decision string
			ch.Receive(ctx, &decision)
			decompTimerCancel()
			decision = strings.ToUpper(strings.TrimSpace(decision))
			if decision == "GO" {
				approved = true
			} else {
				rejected = true
			}
		})

		decompSelector.AddReceive(c.cancelCh, func(ch workflow.ReceiveChannel, more bool) {
			var reason string
			ch.Receive(ctx, &reason)
			decompTimerCancel()
			c.cancelled = true
			c.cancelReason = reason
		})

		decompSelector.AddFuture(decompTimerFuture, func(f workflow.Future) {
			decompTimerCancel()
			c.notify("Decomposition approval waiting. Send `/plan approve` or `/plan realign`.")
		})

		decompSelector.Select(ctx)

		if c.cancelled {
			return false, phaseError{c.cancelledResult()}
		}
	}

	return approved, nil
}

// handoff runs phase 8: create subtasks from the decomposition.
func (c *ceremony) handoff(ctx workflow.Context) (*PlanningResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Phase: handoff to factory", "Steps", len(c.steps))

	handoffCtx := workflow.WithActivityOptions(ctx, c.shortOpts)
	var subtaskIDs []string
	if err := workflow.ExecuteActivity(handoffCtx, c.pa.CreatePlanSubtasksActivity, c.req, c.steps).Get(ctx, &subtaskIDs); err != nil {
		logger.Error("Failed to create subtasks", "error", err)
		c.notify(fmt.Sprintf("Failed to create subtasks: %s", err))
		return &PlanningResult{GoalID: c.req.GoalID, Approaches: c.approaches, SelectedApproach: c.selectedApproach, Cancelled: true, CancelReason: "subtask_creation_failed"}, nil
	}

	c.notify(fmt.Sprintf("Planning complete. %d subtasks created for approach: %s\nSubtask IDs: %s",
		len(subtaskIDs), c.selectedApproach.Title, strings.Join(subtaskIDs, ", ")))

	return &PlanningResult{
		GoalID:           c.req.GoalID,
		SelectedApproach: c.selectedApproach,
		SubtaskIDs:       subtaskIDs,
		Approaches:       c.approaches,
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

func formatDecompSummary(title string, steps []types.DecompStep) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Decomposition for: %s\n\n", title)
	for i, s := range steps {
		fmt.Fprintf(&b, "%d. %s (~%dm)\n   %s\n   Acceptance: %s\n\n",
			i+1, s.Title, s.Estimate, s.Description, s.Acceptance)
	}
	return b.String()
}
