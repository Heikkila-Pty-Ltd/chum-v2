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

	"github.com/Heikkila-Pty-Ltd/chum/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum/internal/dag"
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
	a := &Activities{
		DAG:    d,
		Config: cfg,
		Logger: logger,
	}
	w.RegisterActivity(a)

	da := &DispatchActivities{
		DAG:    d,
		Config: cfg,
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
// Uses tctl CLI — requires Temporal server to be running.
func EnsureNamespace(cfg *config.Config, logger *slog.Logger) error {
	ns := cfg.General.TemporalNamespace
	host := cfg.General.TemporalHostPort

	// Try tctl first
	cmd := exec.CommandContext(context.Background(),
		"tctl", "--address", host, "--namespace", ns,
		"namespace", "register",
		"--retention", "7",
		"--description", "CHUM v2 namespace",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "already registered") ||
			strings.Contains(outStr, "already exists") {
			logger.Info("Namespace already exists", "namespace", ns)
			return nil
		}
		return fmt.Errorf("tctl namespace register: %s: %w", outStr, err)
	}
	logger.Info("Namespace created", "namespace", ns)
	return nil
}
