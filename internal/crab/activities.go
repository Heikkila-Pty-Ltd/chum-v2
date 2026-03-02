package crab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

const prefix = "[CRAB]"

// Activities holds dependencies for CRAB Temporal activities.
type Activities struct {
	DAG    dag.TaskStore
	LLM    llm.Runner
	Agent  string // default agent (claude, gemini, codex)
	Model  string // default model override
}

// ParsePlanActivity performs deterministic markdown parsing of the plan.
func (a *Activities) ParsePlanActivity(ctx context.Context, req DecompositionRequest) (*ParsedPlan, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(prefix+" Parsing plan", "PlanID", req.PlanID)

	plan, err := ParseMarkdownPlan(req.PlanMarkdown)
	if err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}

	logger.Info(prefix+" Plan parsed",
		"Title", plan.Title,
		"ScopeItems", len(plan.ScopeItems),
		"AcceptanceCriteria", len(plan.AcceptanceCriteria),
	)
	return plan, nil
}

// ClarifyGapsActivity performs clarification to resolve ambiguities.
// Tier 1: self-answer from existing morsels. Tier 2: LLM. Tier 3: human.
func (a *Activities) ClarifyGapsActivity(ctx context.Context, req DecompositionRequest, plan ParsedPlan) (*ClarificationResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(prefix+" Clarifying gaps", "PlanID", req.PlanID, "ScopeItems", len(plan.ScopeItems))

	result := &ClarificationResult{}

	// Tier 1: Self-answer from existing morsels
	activity.RecordHeartbeat(ctx, "tier-1-self-answer")

	var existingMorselsSummary strings.Builder
	if a.DAG != nil {
		allTasks, err := a.DAG.ListTasks(ctx, req.Project)
		if err != nil {
			logger.Warn(prefix+" Failed to list tasks", "error", err)
		} else {
			for _, t := range allTasks {
				if t.Status == "open" {
					existingMorselsSummary.WriteString(fmt.Sprintf("- [%s] %s: %s\n", t.Type, t.ID, t.Title))
				}
			}
		}
	}

	var unresolvedItems []ScopeItem
	for _, item := range plan.ScopeItems {
		if item.Completed {
			continue
		}
		unresolvedItems = append(unresolvedItems, item)
	}

	if len(unresolvedItems) == 0 {
		return result, nil
	}

	// Tier 2: Ask LLM
	activity.RecordHeartbeat(ctx, "tier-2-ask-llm")

	var ambiguityList strings.Builder
	for _, item := range unresolvedItems {
		ambiguityList.WriteString(fmt.Sprintf("- %s\n", item.Description))
	}

	prompt := fmt.Sprintf(`You are a senior engineering planner clarifying a decomposition plan.

PLAN: %s
CONTEXT: %s

EXISTING MORSELS IN PROJECT:
%s

UNRESOLVED SCOPE ITEMS (need clarification):
%s

For each unresolved item, either:
1. Provide a clear answer based on context and existing morsels
2. If you cannot answer, respond with "NEEDS_HUMAN: <specific question>"

Respond with ONLY a JSON array:
[{"scope_item": "description", "answer": "clarification or NEEDS_HUMAN: question", "source": "chief_llm"}]`,
		plan.Title,
		types.Truncate(plan.Context, 2000),
		types.Truncate(existingMorselsSummary.String(), 2000),
		ambiguityList.String(),
	)

	cliResult, err := a.LLM.Plan(ctx, a.agent(req.Tier), a.Model, req.WorkDir, prompt)
	if err != nil {
		logger.Warn(prefix+" LLM clarification failed", "error", err)
	} else {
		jsonStr := llm.ExtractJSON(cliResult.Output)
		if jsonStr != "" {
			var answers []struct {
				ScopeItem string `json:"scope_item"`
				Answer    string `json:"answer"`
				Source    string `json:"source"`
			}
			if parseErr := json.Unmarshal([]byte(jsonStr), &answers); parseErr == nil {
				answeredSet := make(map[string]bool)
				for _, ca := range answers {
					if strings.HasPrefix(ca.Answer, "NEEDS_HUMAN:") {
						continue
					}
					result.Resolved = append(result.Resolved, ClarificationEntry{
						Question: fmt.Sprintf("Context for scope item: %s", ca.ScopeItem),
						Answer:   ca.Answer,
						Source:   "chief_llm",
					})
					answeredSet[ca.ScopeItem] = true
				}
				var stillUnresolved []ScopeItem
				for _, item := range unresolvedItems {
					if !answeredSet[item.Description] {
						stillUnresolved = append(stillUnresolved, item)
					}
				}
				unresolvedItems = stillUnresolved
			}
		}
	}

	// Tier 3: Escalate to human
	if len(unresolvedItems) > 0 {
		result.NeedsHumanInput = true
		for _, item := range unresolvedItems {
			result.HumanQuestions = append(result.HumanQuestions,
				fmt.Sprintf("Please clarify scope item: %s", item.Description))
		}
	}

	return result, nil
}

// DecomposeActivity uses an LLM to decompose the parsed plan into whales and morsels.
func (a *Activities) DecomposeActivity(ctx context.Context, req DecompositionRequest, plan ParsedPlan, clarifications ClarificationResult) ([]CandidateWhale, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(prefix+" Decomposing plan", "PlanID", req.PlanID, "Title", plan.Title)

	activity.RecordHeartbeat(ctx, "building-decomposition-prompt")

	var scopeList strings.Builder
	for _, item := range plan.ScopeItems {
		status := "[ ]"
		if item.Completed {
			status = "[x]"
		}
		scopeList.WriteString(fmt.Sprintf("- %s %s\n", status, item.Description))
	}

	var clarificationContext strings.Builder
	for _, entry := range clarifications.Resolved {
		clarificationContext.WriteString(fmt.Sprintf("- Q: %s\n  A: %s (source: %s)\n", entry.Question, entry.Answer, entry.Source))
	}
	if clarifications.HumanAnswers != "" {
		clarificationContext.WriteString(fmt.Sprintf("\nHuman clarifications:\n%s\n", clarifications.HumanAnswers))
	}

	var existingMorsels strings.Builder
	if a.DAG != nil {
		allTasks, err := a.DAG.ListTasks(ctx, req.Project)
		if err == nil {
			count := 0
			for _, t := range allTasks {
				if t.Status == "open" {
					existingMorsels.WriteString(fmt.Sprintf("- [%s|P%d] %s: %s\n", t.Type, t.Priority, t.ID, t.Title))
					count++
					if count >= 30 {
						existingMorsels.WriteString("... and more\n")
						break
					}
				}
			}
		}
	}

	var acList strings.Builder
	for _, ac := range plan.AcceptanceCriteria {
		acList.WriteString(fmt.Sprintf("- %s\n", ac))
	}

	var oosList strings.Builder
	for _, oos := range plan.OutOfScope {
		oosList.WriteString(fmt.Sprintf("- %s\n", oos))
	}

	prompt := fmt.Sprintf(`You are a senior engineering decomposer. Break this plan into whales (epic-level groupings) and morsels (bite-sized executable units).

PLAN: %s
CONTEXT: %s

SCOPE ITEMS:
%s

ACCEPTANCE CRITERIA:
%s

OUT OF SCOPE:
%s

CLARIFICATIONS:
%s

EXISTING MORSELS IN PROJECT:
%s

BLAST RADIUS ANALYSIS:
%s

Rules:
1. Each whale maps to one or more scope items
2. Each morsel must be independently executable by a single agent in one session
3. Morsels should be 15-120 minutes of work
4. Include file hints where possible
5. Specify dependencies between morsels (by index)
6. Do NOT duplicate work already covered by existing morsels
7. STRUCTURAL/FEATURE SPLIT: If a morsel touches >5 files, split into structural (no behavior change) + feature (new behavior) morsels.

Respond with ONLY a JSON array of whales:
[{"index": 0, "title": "...", "description": "...", "acceptance_criteria": "...", "parent_scope_item": 0,
  "morsels": [{"index": 0, "title": "...", "description": "...", "acceptance_criteria": "...", "design_hints": "...", "file_hints": ["..."], "depends_on_indices": []}]}]`,
		plan.Title,
		types.Truncate(plan.Context, 2000),
		scopeList.String(),
		acList.String(),
		oosList.String(),
		clarificationContext.String(),
		types.Truncate(existingMorsels.String(), 2000),
		FormatBlastRadiusSection(req.BlastRadius),
	)

	activity.RecordHeartbeat(ctx, "calling-llm-decompose")

	cliResult, err := a.LLM.Plan(ctx, a.agent(req.Tier), a.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("decomposition LLM call failed: %w", err)
	}

	jsonStr := llm.ExtractJSON(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("decomposition did not produce valid JSON. Output:\n%s", types.Truncate(cliResult.Output, 500))
	}

	var whales []CandidateWhale
	if err := json.Unmarshal([]byte(jsonStr), &whales); err != nil {
		return nil, fmt.Errorf("failed to parse decomposition JSON: %w\nRaw: %s", err, types.Truncate(jsonStr, 500))
	}

	if len(whales) == 0 {
		return nil, fmt.Errorf("decomposition produced zero whales")
	}

	// Cap morsels at 50
	totalMorsels := 0
	for i := range whales {
		totalMorsels += len(whales[i].Morsels)
	}
	if totalMorsels > 50 {
		logger.Warn(prefix+" Excessive morsels, capping", "Total", totalMorsels)
		remaining := 50
		for i := range whales {
			if remaining <= 0 {
				whales[i].Morsels = nil
				continue
			}
			if len(whales[i].Morsels) > remaining {
				whales[i].Morsels = whales[i].Morsels[:remaining]
			}
			remaining -= len(whales[i].Morsels)
		}
	}

	logger.Info(prefix+" Decomposition complete", "Whales", len(whales), "TotalMorsels", totalMorsels)
	return whales, nil
}

// ScopeMorselsActivity reviews morsel scope, splitting oversized ones.
func (a *Activities) ScopeMorselsActivity(ctx context.Context, req DecompositionRequest, whales []CandidateWhale) ([]CandidateWhale, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(prefix+" Scoping morsels", "Whales", len(whales))

	var morselSummary strings.Builder
	for _, whale := range whales {
		morselSummary.WriteString(fmt.Sprintf("\nWhale %d: %s\n", whale.Index, whale.Title))
		for _, morsel := range whale.Morsels {
			morselSummary.WriteString(fmt.Sprintf("  Morsel %d: %s\n    Desc: %s\n    Files: %s\n",
				morsel.Index, morsel.Title, types.Truncate(morsel.Description, 200),
				strings.Join(morsel.FileHints, ", ")))
		}
	}

	prompt := fmt.Sprintf(`You are a senior engineering scope reviewer. Review each morsel and ensure appropriate sizing.

CURRENT MORSELS:
%s

Rules:
1. Each morsel should be 15-120 minutes of work for a single agent
2. If a morsel is too large, split it into smaller morsels
3. If a morsel is too small, note it but don't merge
4. Preserve dependency indices (update if splits occur)
5. Any morsel touching >5 files MUST be split into structural + feature morsels

Respond with ONLY a JSON array of whales (same format as input, with adjustments).`, morselSummary.String())

	cliResult, err := a.LLM.Plan(ctx, a.agent(req.Tier), a.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("scope review failed: %w", err)
	}

	jsonStr := llm.ExtractJSON(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("scope review produced no JSON: %s", types.Truncate(cliResult.Output, 500))
	}

	var scopedWhales []CandidateWhale
	if err := json.Unmarshal([]byte(jsonStr), &scopedWhales); err != nil {
		return nil, fmt.Errorf("failed to parse scope review JSON: %w", err)
	}

	logger.Info(prefix+" Scoping complete", "Whales", len(scopedWhales))
	return scopedWhales, nil
}

// SizeMorselsActivity estimates effort, assigns priority and risk levels.
func (a *Activities) SizeMorselsActivity(ctx context.Context, req DecompositionRequest, whales []CandidateWhale) ([]SizedMorsel, error) {
	logger := activity.GetLogger(ctx)

	totalMorsels := 0
	for i := range whales {
		totalMorsels += len(whales[i].Morsels)
	}
	logger.Info(prefix+" Sizing morsels", "Whales", len(whales), "TotalMorsels", totalMorsels)

	var morselList strings.Builder
	for _, whale := range whales {
		for _, morsel := range whale.Morsels {
			morselList.WriteString(fmt.Sprintf("Whale %d / Morsel %d: %s\n  Description: %s\n  Design hints: %s\n  Files: %s\n  Depends on: %v\n\n",
				whale.Index, morsel.Index, morsel.Title,
				types.Truncate(morsel.Description, 300),
				types.Truncate(morsel.DesignHints, 200),
				strings.Join(morsel.FileHints, ", "),
				morsel.DependsOnIndices,
			))
		}
	}

	prompt := fmt.Sprintf(`You are a senior engineering estimator. Size each morsel with effort, priority, risk, and dependencies.

MORSELS TO SIZE:
%s

For each morsel provide: estimate_minutes (15-120), priority (1-4), risk_level ("low"/"medium"/"high"), sizing_rationale, labels, design (expanded notes), depends_on_indices.

Respond with ONLY a JSON array of sized morsels:
[{"title": "...", "description": "...", "acceptance_criteria": "...", "design": "...", "estimate_minutes": 60, "priority": 2, "labels": ["source:crab"], "file_hints": ["..."], "depends_on_indices": [], "whale_index": 0, "risk_level": "medium", "sizing_rationale": "..."}]`,
		morselList.String(),
	)

	cliResult, err := a.LLM.Plan(ctx, a.agent(req.Tier), a.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("sizing LLM call failed: %w", err)
	}

	jsonStr := llm.ExtractJSON(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("sizing produced no JSON: %s", types.Truncate(cliResult.Output, 500))
	}

	var sizedMorsels []SizedMorsel
	if err := json.Unmarshal([]byte(jsonStr), &sizedMorsels); err != nil {
		return nil, fmt.Errorf("failed to parse sizing JSON: %w", err)
	}

	if len(sizedMorsels) == 0 {
		return nil, fmt.Errorf("sizing produced zero morsels")
	}

	if len(sizedMorsels) > 50 {
		logger.Warn(prefix+" Excessive morsels, capping", "Total", len(sizedMorsels))
		sizedMorsels = sizedMorsels[:50]
	}

	logger.Info(prefix+" Sizing complete", "SizedMorsels", len(sizedMorsels))
	return sizedMorsels, nil
}

// EmitMorselsActivity writes approved whales and morsels to the DAG.
func (a *Activities) EmitMorselsActivity(ctx context.Context, req DecompositionRequest, whales []CandidateWhale, morsels []SizedMorsel) (*EmitResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(prefix+" Emitting to DAG", "Whales", len(whales), "Morsels", len(morsels))

	if a.DAG == nil {
		return nil, fmt.Errorf("DAG is not initialized")
	}

	result := &EmitResult{}

	// Create whale tasks
	whaleIDMap := make(map[int]string)
	for _, whale := range whales {
		parentID := req.ParentWhaleID
		labels := []string{"source:crab", fmt.Sprintf("plan:%s", req.PlanID)}

		whaleID, err := a.DAG.CreateTask(ctx, dag.Task{
			Title:       whale.Title,
			Description: whale.Description,
			Type:        "whale",
			Priority:    1,
			ParentID:    parentID,
			Acceptance:  whale.AcceptanceCriteria,
			Labels:      labels,
			Project:     req.Project,
		})
		if err != nil {
			result.FailedCount++
			result.Details = append(result.Details, fmt.Sprintf("FAILED whale %q: %v", whale.Title, err))
			continue
		}

		whaleIDMap[whale.Index] = whaleID
		result.WhaleIDs = append(result.WhaleIDs, whaleID)
		result.Details = append(result.Details, fmt.Sprintf("OK whale %q -> %s", whale.Title, whaleID))
	}

	// Create morsel tasks
	morselIDMap := make(map[int]string)
	for i, morsel := range morsels {
		whaleID := whaleIDMap[morsel.WhaleIndex]
		if whaleID == "" {
			result.FailedCount++
			result.Details = append(result.Details, fmt.Sprintf("SKIPPED morsel %q: parent whale %d missing", morsel.Title, morsel.WhaleIndex))
			continue
		}

		labels := append(append([]string{}, morsel.Labels...), "source:crab", fmt.Sprintf("plan:%s", req.PlanID))
		if morsel.RiskLevel == "high" {
			labels = append(labels, "risk:high")
		}

		morselID, err := a.DAG.CreateTask(ctx, dag.Task{
			Title:           morsel.Title,
			Description:     morsel.Description,
			Type:            "morsel",
			Priority:        morsel.Priority,
			ParentID:        whaleID,
			Acceptance:      morsel.AcceptanceCriteria,
			EstimateMinutes: morsel.EstimateMinutes,
			Labels:          labels,
			Project:         req.Project,
		})
		if err != nil {
			result.FailedCount++
			result.Details = append(result.Details, fmt.Sprintf("FAILED morsel %q: %v", morsel.Title, err))
			continue
		}

		morselIDMap[i] = morselID
		result.MorselIDs = append(result.MorselIDs, morselID)
		result.Details = append(result.Details, fmt.Sprintf("OK morsel %q -> %s (whale: %s)", morsel.Title, morselID, whaleID))
	}

	// Add dependency edges
	for i, morsel := range morsels {
		fromID := morselIDMap[i]
		if fromID == "" {
			continue
		}
		for _, depIdx := range morsel.DependsOnIndices {
			toID := morselIDMap[depIdx]
			if toID == "" {
				continue
			}
			if err := a.DAG.AddEdge(ctx, fromID, toID); err != nil {
				result.FailedCount++
				result.Details = append(result.Details, fmt.Sprintf("FAILED edge %s->%s: %v", fromID, toID, err))
			}
		}
	}

	logger.Info(prefix+" Emission complete",
		"WhalesCreated", len(result.WhaleIDs),
		"MorselsCreated", len(result.MorselIDs),
		"Failed", result.FailedCount,
	)
	return result, nil
}

// BlastRadiusScanActivity scans project dependencies to inform decomposition.
func (a *Activities) BlastRadiusScanActivity(ctx context.Context, workDir string) (*BlastRadiusReport, error) {
	logger := activity.GetLogger(ctx)
	start := time.Now()
	report := &BlastRadiusReport{}

	activity.RecordHeartbeat(ctx, "listing-source-files")

	files := listSourceFiles(workDir)
	report.TotalFiles = len(files)
	report.Language = detectDominantLanguage(files)

	switch report.Language {
	case "go":
		scanGoBlastRadius(ctx, workDir, report)
	}

	report.ScanDurMs = time.Since(start).Milliseconds()

	logger.Info(prefix+" Blast radius scan complete",
		"Language", report.Language,
		"HotFiles", len(report.HotFiles),
		"TotalPkgs", report.TotalPkgs,
		"DurationMs", report.ScanDurMs,
	)
	return report, nil
}

// FormatBlastRadiusSection returns a text summary for prompt injection.
func FormatBlastRadiusSection(report *BlastRadiusReport) string {
	if report == nil {
		return ""
	}
	if len(report.HotFiles) == 0 && len(report.CircularDeps) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Language: %s | %d packages | %d source files\n\n", report.Language, report.TotalPkgs, report.TotalFiles))

	if len(report.HotFiles) > 0 {
		b.WriteString("High-coupling files (split carefully):\n")
		for _, hf := range report.HotFiles {
			b.WriteString(fmt.Sprintf("- %s (imported by %d packages)\n", hf.Path, hf.ImportedBy))
		}
		b.WriteString("\n")
	}

	if len(report.CircularDeps) > 0 {
		b.WriteString("Circular dependencies (break these if touched):\n")
		for _, cd := range report.CircularDeps {
			b.WriteString(fmt.Sprintf("- %s\n", cd))
		}
	}

	return b.String()
}

// agent returns the agent for the given tier, defaulting to Activities.Agent.
func (a *Activities) agent(tier string) string {
	if a.Agent != "" {
		return a.Agent
	}
	switch tier {
	case "premium":
		return "claude"
	case "balanced":
		return "claude"
	default:
		return "claude"
	}
}

// listSourceFiles collects source file paths under workDir (up to 5000).
func listSourceFiles(workDir string) []string {
	const maxFiles = 5000
	sourceExts := map[string]struct{}{
		".go": {}, ".ts": {}, ".tsx": {}, ".js": {}, ".jsx": {},
		".py": {}, ".rs": {}, ".css": {}, ".scss": {}, ".html": {},
	}

	var files []string
	_ = filepath.WalkDir(workDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			base := d.Name()
			if base == "node_modules" || base == ".git" || base == "vendor" || base == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= maxFiles {
			return filepath.SkipAll
		}
		ext := filepath.Ext(path)
		if _, ok := sourceExts[ext]; ok {
			rel, relErr := filepath.Rel(workDir, path)
			if relErr == nil {
				files = append(files, rel)
			}
		}
		return nil
	})
	return files
}

func detectDominantLanguage(files []string) string {
	counts := map[string]int{}
	for _, f := range files {
		ext := filepath.Ext(f)
		switch ext {
		case ".go":
			counts["go"]++
		case ".ts", ".tsx":
			counts["ts"]++
		case ".js", ".jsx":
			counts["js"]++
		case ".py":
			counts["py"]++
		case ".rs":
			counts["rs"]++
		}
	}

	best := ""
	bestCount := 0
	for lang, count := range counts {
		if count > bestCount {
			best = lang
			bestCount = count
		}
	}
	return best
}

type goListPackage struct {
	ImportPath string   `json:"ImportPath"`
	GoFiles    []string `json:"GoFiles"`
	Imports    []string `json:"Imports"`
}

func scanGoBlastRadius(ctx context.Context, workDir string, report *BlastRadiusReport) {
	result := runShell(ctx, workDir, "go", "list", "-json", "./...")
	if result.err != nil {
		report.ScanErrors = append(report.ScanErrors, "go list failed: "+types.Truncate(result.stderr, 200))
		return
	}

	dec := json.NewDecoder(strings.NewReader(result.stdout))
	var pkgs []goListPackage
	for dec.More() {
		var pkg goListPackage
		if err := dec.Decode(&pkg); err != nil {
			break
		}
		pkgs = append(pkgs, pkg)
	}
	report.TotalPkgs = len(pkgs)

	fanIn := map[string]int{}
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			fanIn[imp]++
		}
	}

	type fanEntry struct {
		path  string
		count int
	}
	var entries []fanEntry
	for path, count := range fanIn {
		if count >= 3 {
			entries = append(entries, fanEntry{path, count})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].path < entries[j].path
	})

	if len(entries) > 20 {
		entries = entries[:20]
	}
	for _, e := range entries {
		report.HotFiles = append(report.HotFiles, HotFile{Path: e.path, ImportedBy: e.count})
	}
}

type shellResult struct {
	stdout string
	stderr string
	err    error
}

func runShell(ctx context.Context, dir string, name string, args ...string) shellResult {
	cmd := execCommand(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return shellResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

// execCommand is a variable for test injection.
var execCommand = defaultExecCommand

func defaultExecCommand(ctx context.Context, name string, args ...string) *execCmd {
	cmd := exec.CommandContext(ctx, name, args...)
	return (*execCmd)(cmd)
}

type execCmd = exec.Cmd
