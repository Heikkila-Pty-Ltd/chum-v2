// CHUM v2 — minimal autonomous agent orchestrator.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/admit"
	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/engine"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
	syncpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/sync"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"

	"go.temporal.io/sdk/client"
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

			// Phase 1: Import from beads
			client, err := beads.NewReadOnlyClient(project.Workspace)
			if err != nil {
				logger.Error("Beads client failed", "project", projectName, "error", err)
				continue
			}
			syncResult, err := syncpkg.SyncToDAG(context.Background(), client, d, projectName, logger)
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
			fmt.Fprintf(os.Stderr, "Usage: chum task create --project NAME --title TITLE [--status STATUS] [--description DESC]\n")
			os.Exit(1)
		}
		var project, title, description, status string
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
			}
		}
		if title == "" || project == "" {
			fmt.Fprintf(os.Stderr, "Error: --project and --title are required\n")
			os.Exit(1)
		}
		if status == "" {
			status = types.StatusReady
		}
		task := dag.Task{
			Title:       title,
			Description: description,
			Status:      status,
			Project:     project,
		}
		id, err := d.CreateTask(context.Background(), task)
		if err != nil {
			logger.Error("Failed to create task", "error", err)
			os.Exit(1)
		}
		fmt.Printf("Created task %s [%s] %s\n", id, status, title)

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
		fmt.Fprintf(os.Stderr, "Usage: chum [serve|sync|tasks|task create|plan|init] [--config path]\n")
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
