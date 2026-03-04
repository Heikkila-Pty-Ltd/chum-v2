// CHUM v2 — minimal autonomous agent orchestrator.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/admit"
	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beadsbridge"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beadsync"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/engine"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/jarvis"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	configPath := "chum.toml"

	// Find config path (--config flag or positional after subcommand)
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
			break
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Info("Beads bridge mode",
		"enabled", cfg.BeadsBridge.Enabled,
		"mode", bridgeMode(cfg),
		"dry_run", cfg.BeadsBridge.DryRun,
		"ingress_policy", cfg.BeadsBridge.IngressPolicy,
		"canary_label", cfg.BeadsBridge.CanaryLabel,
		"reconcile_interval", cfg.BeadsBridge.ReconcileInterval.Duration,
	)

	d, err := dag.Open(cfg.General.DBPath)
	if err != nil {
		logger.Error("Failed to open DAG", "error", err)
		os.Exit(1)
	}
	defer d.Close()

	// Route subcommand
	subcmd := "serve"
	if len(os.Args) > 1 && os.Args[1] != "--config" {
		subcmd = os.Args[1]
	}

	switch subcmd {
	case "serve":
		fmt.Println("CHUM v2 — starting worker")

		// Register Jarvis integration hook.
		engine.RegisterWorkerHook(func(w worker.Worker, c client.Client, d *dag.DAG, cfg *config.Config, logger *slog.Logger) {
			port := cfg.General.JarvisPort
			if port == 0 {
				return
			}

			workDirs := make(map[string]string)
			for name, proj := range cfg.Projects {
				if proj.Enabled {
					workDirs[name] = proj.Workspace
				}
			}

			eng := jarvis.NewEngine(d, c, cfg.General.TaskQueue, workDirs, logger)
			policy := "legacy"
			if cfg.BeadsBridge.Enabled {
				policy = cfg.BeadsBridge.IngressPolicy
			}
			api := &jarvis.API{Engine: eng, Logger: logger, IngressPolicy: policy}

			addr := fmt.Sprintf("127.0.0.1:%d", port)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				logger.Error("Failed to start Jarvis API", "addr", addr, "error", err)
				return
			}

			srv := &http.Server{Handler: api.Handler()}
			go func() {
				logger.Info("Jarvis API listening", "addr", addr)
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					logger.Error("Jarvis API error", "error", err)
				}
			}()
		})

		if err := engine.StartWorker(cfg, d, logger); err != nil {
			logger.Error("Worker failed", "error", err)
			os.Exit(1)
		}

	case "sync":
		fmt.Println("CHUM v2 — syncing from beads")
		astParser := astpkg.NewParser(logger)
		defer astParser.Close()

		for projectName, project := range cfg.Projects {
			if !project.Enabled {
				continue
			}

			if cfg.BeadsBridge.Enabled {
				client, err := beads.NewClient(project.Workspace)
				if err != nil {
					logger.Error("Beads client failed", "project", projectName, "error", err)
					continue
				}
				scanner := &beadsbridge.Scanner{
					DAG:    d,
					Config: cfg.BeadsBridge,
					Logger: logger,
				}
				scanResult, err := scanner.ScanProject(context.Background(), projectName, client)
				if err != nil {
					logger.Error("Bridge scan failed", "project", projectName, "error", err)
					continue
				}
				fmt.Printf("  %s bridge-scan: candidates=%d admitted=%d updated=%d deduped=%d edges=%d dry_run=%t\n",
					projectName, scanResult.Candidates, scanResult.Admitted, scanResult.Updated, scanResult.Deduped, scanResult.EdgesProjected, scanResult.DryRun)
				if !cfg.BeadsBridge.DryRun {
					worker := &beadsbridge.OutboxWorker{DAG: d, Logger: logger}
					processed, outErr := worker.ProcessProject(context.Background(), projectName, client, 100)
					if outErr != nil {
						logger.Error("Bridge outbox failed", "project", projectName, "error", outErr)
					} else if processed > 0 {
						fmt.Printf("  %s bridge-outbox: processed=%d\n", projectName, processed)
					}
				}
				report, recErr := beadsbridge.ReconcileProject(context.Background(), d, client, projectName, false, nil)
				if recErr != nil {
					logger.Error("Bridge reconcile failed", "project", projectName, "error", recErr)
				} else {
					fmt.Printf("  %s bridge-reconcile: drift=%d dry_run=%t\n", projectName, len(report.Items), report.DryRun)
				}
				continue
			}

			// Phase 1: Import from beads
			client, err := beads.NewReadOnlyClient(project.Workspace)
			if err != nil {
				logger.Error("Beads client failed", "project", projectName, "error", err)
				continue
			}
			syncResult, err := beadsync.SyncToDAG(context.Background(), client, d, projectName, logger)
			if err != nil {
				logger.Error("Sync failed", "project", projectName, "error", err)
				continue
			}
			fmt.Printf("  %s sync: %s\n", projectName, syncResult)

			// Phase 2: Admission gate
			gateResult, err := admit.RunGate(context.Background(), d, astParser, projectName, project.Workspace, logger)
			if err != nil {
				logger.Error("Admission gate failed", "project", projectName, "error", err)
				continue
			}
			fmt.Printf("  %s gate: %s\n", projectName, gateResult)
		}

	case "tasks":
		for projectName, project := range cfg.Projects {
			if !project.Enabled {
				continue
			}
			tasks, err := d.ListTasks(context.Background(), projectName)
			if err != nil {
				logger.Error("List failed", "project", projectName, "error", err)
				continue
			}
			fmt.Printf("=== %s (%s) ===\n", projectName, project.Workspace)
			if len(tasks) == 0 {
				fmt.Println("  (no tasks)")
				continue
			}
			for _, t := range tasks {
				fmt.Printf("  [%s] %-12s %s\n", t.Status, t.ID, t.Title)
			}
		}

	case "init":
		fmt.Printf("CHUM v2 — creating Temporal namespace %q\n", cfg.General.TemporalNamespace)
		if err := engine.EnsureNamespace(context.Background(), cfg, logger); err != nil {
			logger.Error("Namespace creation failed", "error", err)
			fmt.Println("  Hint: make sure Temporal server is running and temporal CLI is on PATH")
			os.Exit(1)
		}
		fmt.Println("  Namespace ready")

	case "task":
		if len(os.Args) < 3 || os.Args[2] != "create" {
			fmt.Fprintf(os.Stderr, "Usage: chum task create --project NAME --title TITLE [--description DESC] [--acceptance CRITERIA] [--estimate MINUTES] [--status STATUS]\n")
			os.Exit(1)
		}
		var project, title, description, status, acceptance string
		var estimate int
		systemCaller := false
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--project":
				project = requireFlagValue(os.Args, i)
				i++
			case "--title":
				title = requireFlagValue(os.Args, i)
				i++
			case "--description":
				description = requireFlagValue(os.Args, i)
				i++
			case "--status":
				status = requireFlagValue(os.Args, i)
				i++
			case "--acceptance":
				acceptance = requireFlagValue(os.Args, i)
				i++
			case "--estimate":
				v := requireFlagValue(os.Args, i)
				i++
				fmt.Sscanf(v, "%d", &estimate)
			case "--system":
				systemCaller = true
			}
		}
		if cfg.BeadsBridge.Enabled && cfg.BeadsBridge.IngressPolicy != "legacy" && !systemCaller {
			fmt.Fprintf(os.Stderr, "Error: direct task ingress is blocked by beads bridge policy (%s). Use `chum submit <work.md>`.\n", cfg.BeadsBridge.IngressPolicy)
			os.Exit(1)
		}
		if title == "" || project == "" {
			fmt.Fprintf(os.Stderr, "Error: --project and --title are required\n")
			os.Exit(1)
		}
		if status == "" {
			status = string(types.StatusReady)
		}
		task := dag.Task{
			Title:           title,
			Description:     description,
			Status:          status,
			Project:         project,
			Acceptance:      acceptance,
			EstimateMinutes: estimate,
		}
		id, err := d.CreateTask(context.Background(), task)
		if err != nil {
			logger.Error("Failed to create task", "error", err)
			os.Exit(1)
		}
		fmt.Printf("Created task %s [%s] %s\n", id, status, title)

	case "submit":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: chum submit <work.md> [--project NAME]\n")
			os.Exit(1)
		}
		workFile := os.Args[2]
		project := ""
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--project":
				project = requireFlagValue(os.Args, i)
				i++
			}
		}
		if project == "" {
			project = defaultProject(cfg)
		}
		projCfg, ok := cfg.Projects[project]
		if !ok || !projCfg.Enabled {
			fmt.Fprintf(os.Stderr, "Error: project %q not found or not enabled\n", project)
			os.Exit(1)
		}
		bc, err := beads.NewClient(projCfg.Workspace)
		if err != nil {
			logger.Error("Failed to create beads client", "project", project, "error", err)
			os.Exit(1)
		}
		result, err := beadsbridge.SubmitFromFile(context.Background(), bc, workFile)
		if err != nil {
			logger.Error("Submit failed", "error", err)
			os.Exit(1)
		}
		switch {
		case result.Created:
			fmt.Printf("Submitted %s -> created issue %s (%s)\n", result.FilePath, result.IssueID, result.Title)
		case result.Updated:
			fmt.Printf("Submitted %s -> updated issue %s (%s)\n", result.FilePath, result.IssueID, result.Title)
		default:
			fmt.Printf("Submitted %s -> issue %s (%s)\n", result.FilePath, result.IssueID, result.Title)
		}

	case "reconcile":
		project := ""
		apply := false
		allowRaw := ""
		for i := 2; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--project":
				project = requireFlagValue(os.Args, i)
				i++
			case "--apply":
				apply = true
			case "--allow":
				allowRaw = requireFlagValue(os.Args, i)
				i++
			}
		}
		if project == "" {
			project = defaultProject(cfg)
		}
		projCfg, ok := cfg.Projects[project]
		if !ok || !projCfg.Enabled {
			fmt.Fprintf(os.Stderr, "Error: project %q not found or not enabled\n", project)
			os.Exit(1)
		}
		bc, err := beads.NewClient(projCfg.Workspace)
		if err != nil {
			logger.Error("Failed to create beads client", "project", project, "error", err)
			os.Exit(1)
		}
		allow := parseDriftAllowlist(allowRaw)
		report, err := beadsbridge.ReconcileProject(context.Background(), d, bc, project, apply, allow)
		if err != nil {
			logger.Error("Reconcile failed", "project", project, "error", err)
			os.Exit(1)
		}
		fmt.Printf("Reconcile report project=%s dry_run=%t items=%d\n", report.Project, report.DryRun, len(report.Items))
		for _, item := range report.Items {
			fmt.Printf("  - class=%s issue=%s task=%s action=%s details=%s\n",
				item.Class, item.IssueID, item.TaskID, item.ProposedAction, item.Details)
		}

	case "plan":
		// CLI fallback for planning ceremony (when Matrix is not available)
		var project, agent, goalID string
		for i := 2; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--project":
				project = requireFlagValue(os.Args, i)
				i++
			case "--agent":
				agent = requireFlagValue(os.Args, i)
				i++
			case "--goal":
				goalID = requireFlagValue(os.Args, i)
				i++
			}
		}
		if project == "" || goalID == "" {
			fmt.Fprintf(os.Stderr, "Usage: chum plan --project NAME --goal ISSUE_ID [--agent claude]\n")
			os.Exit(1)
		}
		projCfg, ok := cfg.Projects[project]
		if !ok || !projCfg.Enabled {
			fmt.Fprintf(os.Stderr, "Error: project %q not found or not enabled\n", project)
			os.Exit(1)
		}
		if agent == "" {
			agent = "claude"
		}
		fmt.Printf("CHUM v2 — starting planning ceremony for %s\n", project)

		c, err := engine.DialTemporal(cfg, nil)
		if err != nil {
			logger.Error("Failed to connect to Temporal", "error", err)
			os.Exit(1)
		}
		defer c.Close()

		sessionID := fmt.Sprintf("planning-%s-%d", project, time.Now().UnixNano())
		req := planning.PlanningRequest{
			GoalID:    goalID,
			Project:   project,
			WorkDir:   projCfg.Workspace,
			Agent:     agent,
			Source:    "cli",
			SessionID: sessionID,
		}

		opts := client.StartWorkflowOptions{
			ID:        sessionID,
			TaskQueue: cfg.General.TaskQueue,
		}
		ceremonyCfg := planning.PlanningCeremonyConfig{
			MaxResearchRounds: cfg.Planning.MaxResearchRounds,
			SignalTimeout:     cfg.Planning.SignalTimeout.Duration,
			SessionTimeout:    cfg.Planning.SessionTimeout.Duration,
			MaxCycles:         cfg.Planning.MaxCycles,
		}
		run, err := c.ExecuteWorkflow(context.Background(), opts, planning.PlanningWorkflow, req, ceremonyCfg)
		if err != nil {
			logger.Error("Failed to start planning workflow", "error", err)
			os.Exit(1)
		}
		fmt.Printf("  Planning session started: %s (run: %s)\n", sessionID, run.GetRunID())
		fmt.Println("  Monitor via Matrix chat or wait for completion.")

		var result planning.PlanningResult
		if err := run.Get(context.Background(), &result); err != nil {
			logger.Error("Planning workflow failed", "error", err)
			os.Exit(1)
		}
		if result.Cancelled {
			fmt.Printf("  Planning cancelled: %s\n", result.CancelReason)
		} else {
			fmt.Printf("  Planning complete. %d subtasks created.\n", len(result.SubtaskIDs))
		}

	default:
		fmt.Fprintf(os.Stderr, "Usage: chum [serve|sync|tasks|task create|submit|reconcile|plan|init] [--config path]\n")
		os.Exit(1)
	}
}

// requireFlagValue returns args[i+1] or exits with an error if missing.
func requireFlagValue(args []string, i int) string {
	if i+1 >= len(args) {
		fmt.Fprintf(os.Stderr, "Error: %s requires a value\n", args[i])
		os.Exit(1)
	}
	return args[i+1]
}

func bridgeMode(cfg *config.Config) string {
	if !cfg.BeadsBridge.Enabled {
		return "disabled"
	}
	if cfg.BeadsBridge.DryRun {
		return "dry_run"
	}
	return "active"
}

func defaultProject(cfg *config.Config) string {
	for name, project := range cfg.Projects {
		if project.Enabled {
			return name
		}
	}
	return ""
}

func parseDriftAllowlist(raw string) map[beadsbridge.DriftClass]bool {
	allow := map[beadsbridge.DriftClass]bool{
		beadsbridge.DriftStatusMismatch:     true,
		beadsbridge.DriftDependencyMismatch: true,
	}
	if strings.TrimSpace(raw) == "" {
		return allow
	}
	allow = map[beadsbridge.DriftClass]bool{}
	for _, part := range strings.Split(raw, ",") {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		allow[beadsbridge.DriftClass(v)] = true
	}
	return allow
}
