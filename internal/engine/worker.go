package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/chat"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/watch"
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
	defer c.Close()

	w := worker.New(c, cfg.General.TaskQueue, worker.Options{})

	// Register workflows
	w.RegisterWorkflow(AgentWorkflow)
	w.RegisterWorkflow(DispatcherWorkflow)

	// Register activities
	parser := astpkg.NewParser(logger)

	// Create per-project beads clients for writeback (best-effort — nil if bd unavailable)
	beadsClients := make(map[string]*beads.Client)
	for name, project := range cfg.Projects {
		if !project.Enabled {
			continue
		}
		bc, err := beads.NewClient(project.Workspace)
		if err != nil {
			logger.Warn("Beads client unavailable for project", "project", name, "error", err)
			continue
		}
		beadsClients[name] = bc
	}
	if len(beadsClients) > 0 {
		logger.Info("Beads writeback enabled", "projects", len(beadsClients))
	}

	a := &Activities{
		DAG:          d,
		Config:       cfg,
		Logger:       logger,
		AST:          parser,
		BeadsClients: beadsClients,
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
	w.RegisterActivity(a.DecomposeActivity)
	w.RegisterActivity(a.CreateSubtasksActivity)

	da := &DispatchActivities{
		DAG:    d,
		Config: cfg,
		Logger: logger,
	}
	w.RegisterActivity(da)

	// Register planning workflow + activities
	w.RegisterWorkflow(planning.PlanningWorkflow)

	matrixCfg := chat.MatrixConfig{
		Homeserver:  cfg.General.MatrixHomeserver,
		RoomID:      cfg.General.MatrixRoomID,
		AccessToken: cfg.General.MatrixAccessToken,
	}
	pa := &planning.PlanningActivities{
		DAG:          d,
		Config:       cfg,
		Logger:       logger,
		AST:          parser,
		BeadsClients: beadsClients,
		ChatSend: func(ctx context.Context, roomID, message string) error {
			sendCfg := matrixCfg
			sendCfg.RoomID = roomID
			return chat.SendMatrixMessage(ctx, sendCfg, message)
		},
	}
	w.RegisterActivity(pa.ClarifyGoalActivity)
	w.RegisterActivity(pa.ResearchApproachesActivity)
	w.RegisterActivity(pa.GoalCheckActivity)
	w.RegisterActivity(pa.StoreApproachesActivity)
	w.RegisterActivity(pa.DeeperResearchActivity)
	w.RegisterActivity(pa.AnswerQuestionActivity)
	w.RegisterActivity(pa.DecomposeApproachActivity)
	w.RegisterActivity(pa.CreatePlanSubtasksActivity)
	w.RegisterActivity(pa.NotifyChatActivity)

	// Register Dolt health check workflow + activities
	w.RegisterWorkflow(watch.DoltHealthCheckWorkflow)
	doltActivities := &watch.DoltHealthActivities{
		Logger: logger,
		ChatSend: func(ctx context.Context, roomID, message string) error {
			sendCfg := matrixCfg
			sendCfg.RoomID = roomID
			return chat.SendMatrixMessage(ctx, sendCfg, message)
		},
	}
	w.RegisterActivity(watch.CheckDoltHealthActivity)
	w.RegisterActivity(watch.RestartDoltActivity)
	w.RegisterActivity(doltActivities.AlertDoltFailureActivity)

	// Register dispatcher schedule (idempotent — skips if already exists)
	go func() {
		// Wait for worker to be ready before registering schedules
		time.Sleep(3 * time.Second)
		if err := registerDispatcherSchedule(c, cfg, logger); err != nil {
			logger.Error("Failed to register dispatcher schedule", "error", err)
		}
		// Register Dolt health check schedule if enabled
		if cfg.General.DoltHealthCheckEnabled {
			if err := registerDoltHealthSchedule(c, cfg, logger); err != nil {
				logger.Error("Failed to register dolt health schedule", "error", err)
			}
		}
	}()

	// Start chat bridge if planning is enabled and Matrix is configured.
	// Use a cancellable context so the bridge stops when the worker shuts down.
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	defer bridgeCancel()

	if cfg.Planning.Enabled && matrixCfg.Homeserver != "" && matrixCfg.AccessToken != "" && matrixCfg.RoomID != "" {
		// Resolve default agent from first enabled provider (sorted by name for determinism).
		defaultAgent := "claude"
		providerNames := make([]string, 0, len(cfg.Providers))
		for name := range cfg.Providers {
			providerNames = append(providerNames, name)
		}
		sort.Strings(providerNames)
		for _, name := range providerNames {
			prov := cfg.Providers[name]
			if prov.Enabled && prov.CLI != "" {
				defaultAgent = prov.CLI
				break
			}
		}

		var allowedSenders map[string]bool
		if len(cfg.Planning.AllowedSenders) > 0 {
			allowedSenders = make(map[string]bool, len(cfg.Planning.AllowedSenders))
			for _, s := range cfg.Planning.AllowedSenders {
				allowedSenders[s] = true
			}
			logger.Info("Chat bridge sender allowlist active", "senders", len(allowedSenders))
		}

		projectWorkDirs := make(map[string]string)
		for name, proj := range cfg.Projects {
			if proj.Enabled {
				projectWorkDirs[name] = proj.Workspace
			}
		}

		bridge := &chat.Bridge{
			Client:          c,
			MatrixCfg:       matrixCfg,
			PollInterval:    cfg.Planning.PollInterval.Duration,
			Logger:          logger,
			TaskQueue:       cfg.General.TaskQueue,
			DefaultAgent:    defaultAgent,
			AllowedSenders:  allowedSenders,
			ProjectWorkDirs: projectWorkDirs,
			CeremonyCfg: planning.PlanningCeremonyConfig{
				MaxResearchRounds: cfg.Planning.MaxResearchRounds,
				SignalTimeout:     cfg.Planning.SignalTimeout.Duration,
				SessionTimeout:    cfg.Planning.SessionTimeout.Duration,
				MaxCycles:         cfg.Planning.MaxCycles,
			},
		}
		go func() {
			time.Sleep(3 * time.Second)
			logger.Info("Starting chat bridge for planning")
			if err := bridge.Run(bridgeCtx); err != nil && bridgeCtx.Err() == nil {
				logger.Error("Chat bridge stopped", "error", err)
			}
		}()
	}

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

// registerDoltHealthSchedule creates the Temporal schedule for Dolt health checks.
func registerDoltHealthSchedule(c client.Client, cfg *config.Config, logger *slog.Logger) error {
	interval := cfg.General.DoltHealthCheckInterval.Duration
	if interval <= 0 {
		interval = 30 * time.Second
	}

	healthCfg := watch.DoltHealthConfig{
		DoltDataDir: cfg.General.DoltDataDir,
		Host:        cfg.General.DoltHost,
		Port:        cfg.General.DoltPort,
		MaxRestarts: 3,
		AlertRoomID: cfg.General.MatrixRoomID,
	}

	schedClient := c.ScheduleClient()
	_, err := schedClient.Create(context.Background(), client.ScheduleOptions{
		ID: "chum-v2-dolt-health",
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{
				{Every: interval},
			},
		},
		Action: &client.ScheduleWorkflowAction{
			Workflow:  watch.DoltHealthCheckWorkflow,
			Args:      []interface{}{healthCfg},
			TaskQueue: cfg.General.TaskQueue,
			ID:        "chum-v2-dolt-health-run",
		},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") ||
			strings.Contains(err.Error(), "AlreadyExists") {
			logger.Info("Dolt health schedule already exists", "interval", interval)
			return nil
		}
		return fmt.Errorf("create dolt health schedule: %w", err)
	}

	logger.Info("Dolt health check schedule registered", "interval", interval)
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
