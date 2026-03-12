// Standalone preview server for the CHUM dashboard.
// Serves the frontend + dashboard API endpoints without needing Temporal.
//
// Usage: go run ./cmd/dashboard-preview [--db chum.db] [--port 9780]
package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/jarvis"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
)

func main() {
	dbPath := "chum.db"
	port := "9780"
	webDir := "web"
	configPath := "chum.toml"
	jarvisDB := ""

	for i, arg := range os.Args {
		switch arg {
		case "--config":
			if i+1 < len(os.Args) {
				configPath = os.Args[i+1]
			}
		case "--db":
			if i+1 < len(os.Args) {
				dbPath = os.Args[i+1]
			}
		case "--port":
			if i+1 < len(os.Args) {
				port = os.Args[i+1]
			}
		case "--web":
			if i+1 < len(os.Args) {
				webDir = os.Args[i+1]
			}
		case "--jarvis-db":
			if i+1 < len(os.Args) {
				jarvisDB = os.Args[i+1]
			}
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	workDirs := map[string]string{"chum": "."}
	var cfg *config.Config

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Warn("Failed to load config for project list; using fallback", "config", configPath, "error", err)
	} else {
		discovered := make(map[string]string)
		for name, project := range cfg.Projects {
			if project.Enabled {
				discovered[name] = project.Workspace
			}
		}
		if len(discovered) > 0 {
			workDirs = discovered
		}
	}

	d, err := dag.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer d.Close()

	// Open store against same DB — creates trace/lesson/safety tables if needed.
	s, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open store %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer s.Close()

	eng := jarvis.NewEngine(d, nil, "", workDirs, logger)
	if cfg != nil && cfg.BeadsBridge.Enabled {
		beadsClients := make(map[string]beads.Store)
		for name, workspace := range workDirs {
			bc, bcErr := beads.NewClient(workspace)
			if bcErr != nil {
				logger.Warn("Dashboard preview beads ingress disabled for project (client init failed)",
					"project", name, "error", bcErr)
				continue
			}
			beadsClients[name] = bc
		}
		eng.ConfigureBeadsIngress(cfg.BeadsBridge.IngressPolicy, cfg.BeadsBridge.CanaryLabel, beadsClients)
	}
	// Resolve Jarvis KB path: CLI flag > config > default.
	if jarvisDB == "" && cfg != nil && cfg.General.JarvisKBPath != "" {
		jarvisDB = cfg.General.JarvisKBPath
	}

	runner := llm.CLIRunner{}
	parser := ast.NewParser(logger)
	api := &jarvis.API{Engine: eng, DAG: d, Store: s, LLM: runner, AST: parser, Logger: logger, WebDir: webDir, JarvisKBPath: jarvisDB}

	addr := fmt.Sprintf("0.0.0.0:%s", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to listen on %s: %v\n", addr, err)
		os.Exit(1)
	}

	fmt.Printf("CHUM dashboard: http://%s\n", addr)
	fmt.Printf("  database: %s\n", dbPath)
	fmt.Printf("  web dir:  %s\n", webDir)

	srv := &http.Server{Handler: api.Handler()}
	if err := srv.Serve(ln); err != nil {
		logger.Error("Server error", "error", err)
	}
}
