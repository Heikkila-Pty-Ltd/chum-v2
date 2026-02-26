package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// StartWorker connects to Temporal, registers workflows/activities,
// registers the dispatcher schedule, and starts the worker.
func StartWorker(cfg *config.Config, d *dag.DAG, logger *slog.Logger) error {
	c, err := client.Dial(client.Options{
		HostPort:  cfg.General.TemporalHostPort,
		Namespace: cfg.General.TemporalNamespace,
		Logger:    slogAdapter{logger},
	})
	if err != nil {
		return fmt.Errorf("connect to temporal: %w", err)
	}

	w := worker.New(c, cfg.General.TaskQueue, worker.Options{})

	// Register workflows
	w.RegisterWorkflow(AgentWorkflow)
	w.RegisterWorkflow(DispatcherWorkflow)

	// Register activities
	parser := astpkg.NewParser(logger)

	// Create beads client for writeback (best-effort — nil if bd unavailable)
	var beadsClient *beads.Client
	for _, project := range cfg.Projects {
		if !project.Enabled {
			continue
		}
		bc, err := beads.NewClient(project.Workspace)
		if err != nil {
			logger.Warn("Beads client unavailable, writeback disabled", "error", err)
		} else {
			beadsClient = bc
		}
		break
	}

	a := &Activities{
		DAG:         d,
		Config:      cfg,
		Logger:      logger,
		AST:         parser,
		BeadsClient: beadsClient,
	}
	// Register activities explicitly so additions are visible and reviewable.
	w.RegisterActivity(a.SetupWorktreeActivity)
	w.RegisterActivity(a.ExecuteActivity)
	w.RegisterActivity(a.DoDCheckActivity)
	w.RegisterActivity(a.PushActivity)
	w.RegisterActivity(a.CreatePRActivity)
	w.RegisterActivity(a.CreatePRInfoActivity)
	w.RegisterActivity(a.GetPRInfoActivity)
	w.RegisterActivity(a.CloseTaskActivity)
	w.RegisterActivity(a.CloseTaskWithDetailActivity)
	w.RegisterActivity(a.CleanupWorktreeActivity)
	w.RegisterActivity(a.RunReviewActivity)
	w.RegisterActivity(a.SubmitReviewActivity)
	w.RegisterActivity(a.CheckPRStateActivity)
	w.RegisterActivity(a.ReadReviewFeedbackActivity)
	w.RegisterActivity(a.MergePRActivity)
	w.RegisterActivity(a.GuardReviewerCleanActivity)
	w.RegisterActivity(a.ResolveReviewerLoginActivity)
	w.RegisterActivity(a.NotifyActivity)

	da := &DispatchActivities{
		DAG:    d,
		Config: cfg,
		Logger: logger,
	}
	w.RegisterActivity(da)

	// Register dispatcher schedule (idempotent — skips if already exists)
	go func() {
		// Wait for worker to be ready before registering schedules
		time.Sleep(3 * time.Second)
		if err := registerDispatcherSchedule(c, cfg, logger); err != nil {
			logger.Error("Failed to register dispatcher schedule", "error", err)
		}
	}()

	logger.Info("Starting CHUM v2 worker",
		"task_queue", cfg.General.TaskQueue,
		"namespace", cfg.General.TemporalNamespace,
	)
	return w.Run(worker.InterruptCh())
}

// registerDispatcherSchedule creates (or detects existing) the Temporal
// schedule that triggers DispatcherWorkflow on each tick.
func registerDispatcherSchedule(c client.Client, cfg *config.Config, logger *slog.Logger) error {
	tickInterval := cfg.General.TickInterval.Duration
	if tickInterval <= 0 {
		tickInterval = 2 * time.Minute
	}

	schedClient := c.ScheduleClient()
	_, err := schedClient.Create(context.Background(), client.ScheduleOptions{
		ID: "chum-v2-dispatcher",
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{
				{Every: tickInterval},
			},
		},
		Action: &client.ScheduleWorkflowAction{
			Workflow:  DispatcherWorkflow,
			Args:      []interface{}{struct{}{}},
			TaskQueue: cfg.General.TaskQueue,
			ID:        "chum-v2-dispatcher-run",
		},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if err != nil {
		// Schedule may already exist from a previous run
		if strings.Contains(err.Error(), "already exists") ||
			strings.Contains(err.Error(), "AlreadyExists") {
			logger.Info("Dispatcher schedule already exists", "interval", tickInterval)
			return nil
		}
		return fmt.Errorf("create dispatcher schedule: %w", err)
	}

	logger.Info("Dispatcher schedule registered", "interval", tickInterval)
	return nil
}

// slogAdapter wraps slog.Logger to satisfy Temporal's log.Logger interface.
type slogAdapter struct {
	l *slog.Logger
}

func (s slogAdapter) Debug(msg string, keysAndValues ...interface{}) {
	s.l.Debug(msg, keysAndValues...)
}
func (s slogAdapter) Info(msg string, keysAndValues ...interface{}) {
	s.l.Info(msg, keysAndValues...)
}
func (s slogAdapter) Warn(msg string, keysAndValues ...interface{}) {
	s.l.Warn(msg, keysAndValues...)
}
func (s slogAdapter) Error(msg string, keysAndValues ...interface{}) {
	s.l.Error(msg, keysAndValues...)
}

// EnsureNamespace creates the Temporal namespace if it doesn't exist.
// Uses temporal CLI — requires Temporal server to be running.
func EnsureNamespace(cfg *config.Config, logger *slog.Logger) error {
	ns := cfg.General.TemporalNamespace
	host := cfg.General.TemporalHostPort

	cmd := exec.CommandContext(context.Background(),
		"temporal", "operator", "namespace", "create",
		"--address", host,
		"--namespace", ns,
		"--retention", "7d",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "already registered") ||
			strings.Contains(outStr, "already exists") ||
			strings.Contains(outStr, "Namespace is already registered") {
			logger.Info("Namespace already exists", "namespace", ns)
			return nil
		}
		return fmt.Errorf("namespace create: %s: %w", outStr, err)
	}
	logger.Info("Namespace created", "namespace", ns)
	return nil
}
