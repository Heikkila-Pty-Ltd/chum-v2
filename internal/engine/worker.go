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
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/perf"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/watch"
)

// WorkerHook is called during worker setup to register additional workflows/activities.
type WorkerHook func(w worker.Worker, c client.Client, d *dag.DAG, cfg *config.Config, logger *slog.Logger)

var workerHooks []WorkerHook

// RegisterWorkerHook adds a hook that runs during worker setup.
// Call before StartWorker.
func RegisterWorkerHook(hook WorkerHook) {
	workerHooks = append(workerHooks, hook)
}

// StartWorker connects to Temporal, registers workflows/activities,
// registers the dispatcher schedule, and starts the worker.
func StartWorker(cfg *config.Config, d *dag.DAG, logger *slog.Logger) error {
	c, err := DialTemporal(cfg, logger)
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}
	defer c.Close()

	w := worker.New(c, cfg.General.TaskQueue, worker.Options{})

	// Open trace store and perf tracker (best-effort — worker runs without tracing).
	traceStore, tracker, err := openTraceStore(cfg, logger)
	if err != nil {
		logger.Warn("Trace store unavailable, continuing without tracing", "error", err)
	}
	if traceStore != nil {
		defer traceStore.Close()
	}

	parser := astpkg.NewParser(logger)
	beadsClients := buildBeadsClients(cfg, logger)
	chatSender := buildChatSender(cfg, logger)

	registerEngineWorkflows(w, d, cfg, logger, parser, beadsClients, chatSender, traceStore, tracker, c)
	registerPlanningWorkflows(w, d, cfg, logger, parser, beadsClients, chatSender)
	registerHealthWorkflows(w, logger, chatSender)

	// Allow external packages to register additional workflows/activities.
	for _, hook := range workerHooks {
		hook(w, c, d, cfg, logger)
	}

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	go registerSchedules(shutdownCtx, c, cfg, d, logger)
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
	beadsClients map[string]beads.Store, chatSender notify.ChatSender,
	traceStore store.TraceStore, tracker *perf.Tracker, temporalClient client.Client) {

	w.RegisterWorkflow(AgentWorkflow)
	w.RegisterWorkflow(DispatcherWorkflow)
	w.RegisterWorkflow(ReviewWorkflow)

	a := &Activities{
		DAG:          d,
		Config:       cfg,
		Logger:       logger,
		AST:          parser,
		BeadsClients: beadsClients,
		ChatSend:     chatSender,
		LLM:          buildRetryRunner(cfg, logger),
		Traces:       traceStore,
		Perf:         tracker,
	}
	w.RegisterActivity(a.SetupWorktreeActivity)
	w.RegisterActivity(a.SetupWorktreeFromRefActivity)
	w.RegisterActivity(a.ExecuteActivity)
	w.RegisterActivity(a.DoDCheckActivity)
	w.RegisterActivity(a.PushActivity)
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
	w.RegisterActivity(a.RecordTraceActivity)

	da := &DispatchActivities{
		DAG:          d,
		Config:       cfg,
		Logger:       logger,
		Perf:         tracker,
		BeadsClients: beadsClients,
		Temporal:     temporalClient,
	}
	w.RegisterActivity(da)
}

// registerPlanningWorkflows registers the planning ceremony workflow and activities.
func registerPlanningWorkflows(w worker.Worker, d *dag.DAG, cfg *config.Config,
	logger *slog.Logger, parser *astpkg.Parser,
	beadsClients map[string]beads.Store, chatSender notify.ChatSender) {

	w.RegisterWorkflow(planning.PlanningWorkflow)

	pa := &planning.PlanningActivities{
		DAG:          d,
		Decisions:    d,
		Planning:     d,
		Config:       cfg,
		Logger:       logger,
		AST:          parser,
		BeadsClients: beadsClients,
		ChatSend:     chatSender,
		LLM:          buildRetryRunner(cfg, logger),
	}
	w.RegisterActivity(pa.ClarifyGoalActivity)
	w.RegisterActivity(pa.ResearchApproachesActivity)
	w.RegisterActivity(pa.GoalCheckActivity)
	w.RegisterActivity(pa.StoreApproachesActivity)
	w.RegisterActivity(pa.DeeperResearchActivity)
	w.RegisterActivity(pa.AnswerQuestionActivity)
	w.RegisterActivity(pa.DecomposeApproachActivity)
	w.RegisterActivity(pa.BuildPlanSpecActivity)
	w.RegisterActivity(pa.CreatePlanSubtasksActivity)
	w.RegisterActivity(pa.RecordPlanningDecisionActivity)
	w.RegisterActivity(pa.StorePlanningSnapshotActivity)
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
func registerSchedules(ctx context.Context, c client.Client, cfg *config.Config, d *dag.DAG, logger *slog.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}

	// Canonical defaults are applied in config.Load(); no runtime fallback needed.
	tickInterval := cfg.General.TickInterval.Duration
	if err := RegisterSchedule(ctx, c, ScheduleSpec{
		ID:        DispatcherScheduleID,
		Interval:  tickInterval,
		Workflow:  DispatcherWorkflow,
		Args:      []interface{}{struct{}{}},
		TaskQueue: cfg.General.TaskQueue,
		RunID:     "chum-v2-dispatcher-run",
		Paused:    cfg.General.Paused,
		PauseDB:   d,
	}, logger); err != nil {
		logger.Error("Failed to register dispatcher schedule", "error", err)
	}

	if cfg.General.DoltHealthCheckEnabled {
		healthInterval := cfg.General.DoltHealthCheckInterval.Duration
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

// openTraceStore opens the trace database and creates a perf tracker.
func openTraceStore(cfg *config.Config, logger *slog.Logger) (*store.Store, *perf.Tracker, error) {
	s, err := store.Open(cfg.General.TracesDBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open traces db %s: %w", cfg.General.TracesDBPath, err)
	}
	if err := perf.Migrate(s.DB()); err != nil {
		s.Close()
		return nil, nil, fmt.Errorf("migrate perf schema: %w", err)
	}
	t := perf.New(s.DB(), 0) // default exploration factor
	logger.Info("Trace store and perf tracker initialized", "path", cfg.General.TracesDBPath)
	return s, t, nil
}

// buildRetryRunner creates an LLM Runner with retry and provider fallback from config.
func buildRetryRunner(cfg *config.Config, logger *slog.Logger) llm.Runner {
	if cfg == nil {
		return llm.CLIRunner{}
	}

	// Convert config providers to llm.ProviderConfig map.
	providers := make(map[string]llm.ProviderConfig, len(cfg.Providers))
	for name, p := range cfg.Providers {
		providers[name] = llm.ProviderConfig{
			CLI:     p.CLI,
			Model:   p.Model,
			Tier:    p.Tier,
			Enabled: p.Enabled,
		}
	}

	tiers := llm.TierConfig{
		Fast:     cfg.Tiers.Fast,
		Balanced: cfg.Tiers.Balanced,
		Premium:  cfg.Tiers.Premium,
	}

	selector := llm.NewConfigSelector(providers, tiers)
	return llm.NewRetryRunner(llm.CLIRunner{}, selector, logger)
}
