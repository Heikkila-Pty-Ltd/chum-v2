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

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/jarvis"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
)

func main() {
	dbPath := "chum.db"
	port := "9780"
	webDir := "web"

	for i, arg := range os.Args {
		switch arg {
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
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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

	workDirs := map[string]string{"chum": "."}
	eng := jarvis.NewEngine(d, nil, "", workDirs, logger)
	runner := llm.CLIRunner{}
	api := &jarvis.API{Engine: eng, DAG: d, Store: s, LLM: runner, Logger: logger, WebDir: webDir}

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
