package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/chat"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/notify"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/watch"
)

// StartWorker connects to Temporal, registers workflows/activities,
// registers the dispatcher schedule, and starts the worker.
func StartWorker(cfg *config.Config, d *dag.DAG, logger *slog.Logger) error {
	c, err := DialTemporal(cfg, logger)
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}
	defer c.Close()

	w := worker.New(c, cfg.General.TaskQueue, worker.Options{})

	parser := astpkg.NewParser(logger)
	beadsClients := buildBeadsClients(cfg, logger)
	chatSender := buildChatSender(cfg, logger)

	registerEngineWorkflows(w, d, cfg, logger, parser, beadsClients, chatSender)
	registerPlanningWorkflows(w, d, cfg, logger, parser, beadsClients, chatSender)
	registerHealthWorkflows(w, logger, chatSender)

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	go registerSchedules(shutdownCtx, c, cfg, logger)
	startBridge(shutdownCtx, c, cfg, logger)

	logger.Info("Starting CHUM v2 worker",
		"task_queue", cfg.General.TaskQueue,
		"namespace", cfg.General.TemporalNamespace,
	)
	return w.Run(worker.InterruptCh())
}

// buildBeadsClients creates per-project beads clients for writeback.
// Projects where bd is unavailable get a NullStore guard.
func buildBeadsClients(cfg *config.Config, logger *slog.Logger) map[string]beads.Store {
	clients := make(map[string]beads.Store)
	for name, project := range cfg.Projects {
		if !project.Enabled {
			continue
		}
		bc, err := beads.NewClient(project.Workspace)
		if err != nil {
			logger.Warn("Beads client unavailable for project, using NullStore", "project", name, "error", err)
			clients[name] = &beads.NullStore{Logger: logger}
			continue
		}
		clients[name] = bc
	}
	logger.Info("Beads clients initialized", "projects", len(clients))
	return clients
}

// buildChatSender creates the appropriate ChatSender based on config.
func buildChatSender(cfg *config.Config, logger *slog.Logger) notify.ChatSender {
	switch {
	case strings.TrimSpace(cfg.General.MatrixWebhookURL) != "":
		return notify.NewWebhookSender(cfg.General.MatrixWebhookURL)
	case cfg.General.MatrixHomeserver != "" && cfg.General.MatrixAccessToken != "":
		return notify.NewMatrixSender(cfg.General.MatrixHomeserver, cfg.General.MatrixAccessToken)
	default:
		return &notify.NullSender{Logger: logger}
	}
}

// registerEngineWorkflows registers the core agent and dispatcher workflows/activities.
func registerEngineWorkflows(w worker.Worker, d dag.TaskStore, cfg *config.Config,
	logger *slog.Logger, parser *astpkg.Parser,
	beadsClients map[string]beads.Store, chatSender notify.ChatSender) {

	w.RegisterWorkflow(AgentWorkflow)
	w.RegisterWorkflow(DispatcherWorkflow)

	a := &Activities{
		DAG:          d,
		Config:       cfg,
		Logger:       logger,
		AST:          parser,
		BeadsClients: beadsClients,
		ChatSend:     chatSender,
		LLM:          llm.CLIRunner{},
	}
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
}

// registerPlanningWorkflows registers the planning ceremony workflow and activities.
func registerPlanningWorkflows(w worker.Worker, d dag.TaskStore, cfg *config.Config,
	logger *slog.Logger, parser *astpkg.Parser,
	beadsClients map[string]beads.Store, chatSender notify.ChatSender) {

	w.RegisterWorkflow(planning.PlanningWorkflow)

	pa := &planning.PlanningActivities{
		DAG:          d,
		Config:       cfg,
		Logger:       logger,
		AST:          parser,
		BeadsClients: beadsClients,
		ChatSend:     chatSender,
		LLM:          llm.CLIRunner{},
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
}

// registerHealthWorkflows registers the Dolt health check workflow and activities.
func registerHealthWorkflows(w worker.Worker, logger *slog.Logger, chatSender notify.ChatSender) {
	w.RegisterWorkflow(watch.DoltHealthCheckWorkflow)
	doltActivities := &watch.DoltHealthActivities{
		Logger:   logger,
		ChatSend: chatSender,
	}
	w.RegisterActivity(watch.CheckDoltHealthActivity)
	w.RegisterActivity(watch.RestartDoltActivity)
	w.RegisterActivity(doltActivities.AlertDoltFailureActivity)
}

// registerSchedules registers Temporal schedules after a startup delay.
func registerSchedules(ctx context.Context, c client.Client, cfg *config.Config, logger *slog.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}

	// Canonical defaults are applied in config.Load(); these guards are defensive
	// against zero-value config.Config{} from tests or other edge cases.
	tickInterval := cfg.General.TickInterval.Duration
	if tickInterval <= 0 {
		tickInterval = 2 * time.Minute
	}
	if err := RegisterSchedule(ctx, c, ScheduleSpec{
		ID:        "chum-v2-dispatcher",
		Interval:  tickInterval,
		Workflow:  DispatcherWorkflow,
		Args:      []interface{}{struct{}{}},
		TaskQueue: cfg.General.TaskQueue,
		RunID:     "chum-v2-dispatcher-run",
	}, logger); err != nil {
		logger.Error("Failed to register dispatcher schedule", "error", err)
	}

	if cfg.General.DoltHealthCheckEnabled {
		healthInterval := cfg.General.DoltHealthCheckInterval.Duration
		if healthInterval <= 0 {
			healthInterval = 30 * time.Second
		}
		if err := RegisterSchedule(ctx, c, ScheduleSpec{
			ID:       "chum-v2-dolt-health",
			Interval: healthInterval,
			Workflow: watch.DoltHealthCheckWorkflow,
			Args: []interface{}{watch.DoltHealthConfig{
				DoltDataDir: cfg.General.DoltDataDir,
				Host:        cfg.General.DoltHost,
				Port:        cfg.General.DoltPort,
				MaxRestarts: 3,
				AlertRoomID: cfg.General.MatrixRoomID,
			}},
			TaskQueue: cfg.General.TaskQueue,
			RunID:     "chum-v2-dolt-health-run",
		}, logger); err != nil {
			logger.Error("Failed to register dolt health schedule", "error", err)
		}
	}
}

// startBridge starts the chat bridge if planning is enabled and Matrix is configured.
func startBridge(ctx context.Context, c client.Client, cfg *config.Config, logger *slog.Logger) {
	matrixCfg := chat.MatrixConfig{
		Homeserver:  cfg.General.MatrixHomeserver,
		RoomID:      cfg.General.MatrixRoomID,
		AccessToken: cfg.General.MatrixAccessToken,
	}
	if !cfg.Planning.Enabled || matrixCfg.Homeserver == "" || matrixCfg.AccessToken == "" || matrixCfg.RoomID == "" {
		return
	}

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
		if err := bridge.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("Chat bridge stopped", "error", err)
		}
	}()
}

// EnsureNamespace creates the Temporal namespace if it doesn't exist.
// Uses temporal CLI — requires Temporal server to be running.
func EnsureNamespace(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	ns := cfg.General.TemporalNamespace
	host := cfg.General.TemporalHostPort

	cmd := exec.CommandContext(ctx,
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
