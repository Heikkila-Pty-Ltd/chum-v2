package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dashboardaudit"
)

func main() {
	repoRoot := "."
	if len(os.Args) > 1 {
		repoRoot = os.Args[1]
	}

	report, err := dashboardaudit.Analyze(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard-prune-report: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "dashboard-prune-report: encode report: %v\n", err)
		os.Exit(1)
	}
}
