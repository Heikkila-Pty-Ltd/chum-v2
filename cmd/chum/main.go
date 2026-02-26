// CHUM v2 — minimal autonomous agent orchestrator.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/admit"
	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/engine"
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
			syncResult, err := beads.SyncToDAG(context.Background(), client, d, projectName, logger)
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
		if err := engine.EnsureNamespace(cfg, logger); err != nil {
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
		for i := 3; i < len(os.Args)-1; i++ {
			switch os.Args[i] {
			case "--project":
				project = os.Args[i+1]
			case "--title":
				title = os.Args[i+1]
			case "--description":
				description = os.Args[i+1]
			case "--status":
				status = os.Args[i+1]
			}
		}
		if title == "" || project == "" {
			fmt.Fprintf(os.Stderr, "Error: --project and --title are required\n")
			os.Exit(1)
		}
		if status == "" {
			status = "ready"
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

	default:
		fmt.Fprintf(os.Stderr, "Usage: chum [serve|sync|tasks|task create|init] [--config path]\n")
		os.Exit(1)
	}
}
