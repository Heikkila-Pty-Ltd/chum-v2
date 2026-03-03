// Package genome implements species-based learning for the code generation pipeline.
//
// A "species" is a configuration of agent + model + prompt strategy + tool set.
// Each execution trace records which species was used, what tools were called,
// and whether the run succeeded. The genome package analyzes this history to:
//
//   - Track fitness scores per species (success rate, avg reward, speed)
//   - Extract winning tool sequences from successful runs
//   - Recommend which species to use for a given task type
//   - Mutate species configs based on observed patterns
//
// This closes the learning loop: trace data flows in, better agent configs flow out.
package genome

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// Species represents an agent configuration that can be evaluated and evolved.
type Species struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Agent       string    `json:"agent"`       // CLI tool: "claude", "codex", etc.
	Model       string    `json:"model"`       // Model name: "sonnet-4", "gpt-4.1", etc.
	Tier        string    `json:"tier"`        // fast/balanced/premium
	PromptStyle string    `json:"prompt_style"` // minimal/detailed/chain-of-thought
	ToolSet     []string  `json:"tool_set"`    // Allowed tools for this species
	CreatedAt   time.Time `json:"created_at"`
	ParentID    string    `json:"parent_id,omitempty"` // If mutated from another species
}

// Fitness tracks the performance of a species over time.
type Fitness struct {
	SpeciesID    string  `json:"species_id"`
	TotalRuns    int     `json:"total_runs"`
	Successes    int     `json:"successes"`
	Failures     int     `json:"failures"`
	SuccessRate  float64 `json:"success_rate"`
	AvgReward    float64 `json:"avg_reward"`
	AvgDurationS float64 `json:"avg_duration_s"`
	AvgTokens    int     `json:"avg_tokens"`
	// UCB1 exploration bonus: encourages trying under-explored species.
	// Score = SuccessRate + C * sqrt(ln(TotalAllRuns) / TotalRuns)
	UCBScore float64 `json:"ucb_score"`
}

// ToolPattern is a recurring sequence of tool calls observed in successful runs.
type ToolPattern struct {
	Sequence []string `json:"sequence"`
	Count    int      `json:"count"`
	AvgReward float64 `json:"avg_reward"`
}

// Recommendation is a species selection suggestion for a task.
type Recommendation struct {
	SpeciesID  string  `json:"species_id"`
	Species    Species `json:"species"`
	Fitness    Fitness `json:"fitness"`
	Confidence float64 `json:"confidence"` // 0-1, based on sample size
	Reason     string  `json:"reason"`
}

// Store defines the persistence interface for genome data.
type Store interface {
	// Species CRUD
	CreateSpecies(ctx context.Context, s Species) error
	GetSpecies(ctx context.Context, id string) (*Species, error)
	ListSpecies(ctx context.Context) ([]Species, error)

	// Fitness tracking
	RecordRun(ctx context.Context, speciesID string, success bool, reward float64, durationS float64, tokens int) error
	GetFitness(ctx context.Context, speciesID string) (*Fitness, error)
	GetAllFitness(ctx context.Context) ([]Fitness, error)

	// Tool patterns (extracted from trace data)
	RecordToolPattern(ctx context.Context, speciesID string, sequence []string, reward float64) error
	GetTopToolPatterns(ctx context.Context, speciesID string, limit int) ([]ToolPattern, error)
}

// Engine is the main genome learning engine.
type Engine struct {
	store      Store
	exploration float64 // UCB1 exploration constant (default: 1.414)
}

// NewEngine creates a genome engine with the given store.
func NewEngine(store Store) *Engine {
	return &Engine{
		store:      store,
		exploration: math.Sqrt2,
	}
}

// SetExploration overrides the UCB1 exploration constant.
// Higher values = more exploration of under-tested species.
func (e *Engine) SetExploration(c float64) {
	e.exploration = c
}

// RegisterSpecies adds a new species configuration.
func (e *Engine) RegisterSpecies(ctx context.Context, s Species) error {
	if s.ID == "" {
		s.ID = fmt.Sprintf("%s-%s-%s", s.Agent, s.Model, s.Tier)
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	return e.store.CreateSpecies(ctx, s)
}

// RecordOutcome records the result of a species execution.
func (e *Engine) RecordOutcome(ctx context.Context, speciesID string, success bool, reward float64, durationS float64, tokens int) error {
	return e.store.RecordRun(ctx, speciesID, success, reward, durationS, tokens)
}

// RecordToolSequence extracts and stores the tool call pattern from a run.
func (e *Engine) RecordToolSequence(ctx context.Context, speciesID string, tools []string, reward float64) error {
	if len(tools) == 0 {
		return nil
	}
	// Store the full sequence
	if err := e.store.RecordToolPattern(ctx, speciesID, tools, reward); err != nil {
		return err
	}
	// Also extract subsequences of length 2-5 for pattern mining
	for windowSize := 2; windowSize <= min(5, len(tools)); windowSize++ {
		for i := 0; i <= len(tools)-windowSize; i++ {
			subseq := tools[i : i+windowSize]
			if err := e.store.RecordToolPattern(ctx, speciesID, subseq, reward); err != nil {
				return err
			}
		}
	}
	return nil
}

// Recommend returns the best species for a task, using UCB1 to balance
// exploitation (use what works) with exploration (try under-tested species).
func (e *Engine) Recommend(ctx context.Context, taskTier string) (*Recommendation, error) {
	allFitness, err := e.store.GetAllFitness(ctx)
	if err != nil {
		return nil, fmt.Errorf("genome: get fitness: %w", err)
	}

	if len(allFitness) == 0 {
		return nil, fmt.Errorf("genome: no species registered")
	}

	// Calculate total runs across all species for UCB1
	totalRuns := 0
	for _, f := range allFitness {
		totalRuns += f.TotalRuns
	}

	// Score each species using UCB1
	var bestFitness *Fitness
	bestScore := -1.0

	for i := range allFitness {
		f := &allFitness[i]

		// Filter by tier if specified
		species, err := e.store.GetSpecies(ctx, f.SpeciesID)
		if err != nil {
			continue
		}
		if taskTier != "" && species.Tier != taskTier {
			continue
		}

		if f.TotalRuns == 0 {
			// Never tested = infinite exploration bonus = try it first
			f.UCBScore = math.Inf(1)
		} else {
			exploitScore := f.SuccessRate
			exploreScore := e.exploration * math.Sqrt(math.Log(float64(totalRuns+1))/float64(f.TotalRuns))
			f.UCBScore = exploitScore + exploreScore
		}

		if f.UCBScore > bestScore {
			bestScore = f.UCBScore
			bestFitness = f
		}
	}

	if bestFitness == nil {
		return nil, fmt.Errorf("genome: no species match tier %q", taskTier)
	}

	species, err := e.store.GetSpecies(ctx, bestFitness.SpeciesID)
	if err != nil {
		return nil, fmt.Errorf("genome: get species %q: %w", bestFitness.SpeciesID, err)
	}

	// Confidence is based on sample size — more runs = more confidence
	confidence := 1.0 - 1.0/(1.0+float64(bestFitness.TotalRuns)/10.0)

	reason := fmt.Sprintf("UCB1 score %.3f (success %.0f%%, %d runs, explore bonus %.3f)",
		bestFitness.UCBScore, bestFitness.SuccessRate*100, bestFitness.TotalRuns,
		bestFitness.UCBScore-bestFitness.SuccessRate)

	return &Recommendation{
		SpeciesID:  bestFitness.SpeciesID,
		Species:    *species,
		Fitness:    *bestFitness,
		Confidence: confidence,
		Reason:     reason,
	}, nil
}

// GetWinningPatterns returns the most successful tool sequences for a species.
func (e *Engine) GetWinningPatterns(ctx context.Context, speciesID string) ([]ToolPattern, error) {
	return e.store.GetTopToolPatterns(ctx, speciesID, 10)
}

// Mutate creates a new species by modifying an existing one.
// This is the "evolution" step — take what works and tweak it.
func (e *Engine) Mutate(ctx context.Context, parentID string, mutations map[string]string) (*Species, error) {
	parent, err := e.store.GetSpecies(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("genome: get parent species: %w", err)
	}

	child := Species{
		Agent:       parent.Agent,
		Model:       parent.Model,
		Tier:        parent.Tier,
		PromptStyle: parent.PromptStyle,
		ToolSet:     append([]string{}, parent.ToolSet...),
		ParentID:    parentID,
		CreatedAt:   time.Now(),
	}

	// Apply mutations
	for key, val := range mutations {
		switch key {
		case "agent":
			child.Agent = val
		case "model":
			child.Model = val
		case "tier":
			child.Tier = val
		case "prompt_style":
			child.PromptStyle = val
		case "add_tool":
			child.ToolSet = append(child.ToolSet, val)
		case "remove_tool":
			filtered := make([]string, 0, len(child.ToolSet))
			for _, t := range child.ToolSet {
				if t != val {
					filtered = append(filtered, t)
				}
			}
			child.ToolSet = filtered
		}
	}

	child.Name = fmt.Sprintf("%s-%s-%s-mut", child.Agent, child.Model, child.Tier)
	child.ID = fmt.Sprintf("%s-%d", child.Name, time.Now().UnixMilli())

	if err := e.store.CreateSpecies(ctx, child); err != nil {
		return nil, fmt.Errorf("genome: create mutant: %w", err)
	}

	return &child, nil
}

// Leaderboard returns all species sorted by fitness score.
func (e *Engine) Leaderboard(ctx context.Context) ([]Recommendation, error) {
	allFitness, err := e.store.GetAllFitness(ctx)
	if err != nil {
		return nil, err
	}

	totalRuns := 0
	for _, f := range allFitness {
		totalRuns += f.TotalRuns
	}

	var board []Recommendation
	for _, f := range allFitness {
		species, err := e.store.GetSpecies(ctx, f.SpeciesID)
		if err != nil {
			continue
		}

		if f.TotalRuns == 0 {
			f.UCBScore = math.Inf(1)
		} else {
			f.UCBScore = f.SuccessRate + e.exploration*math.Sqrt(math.Log(float64(totalRuns+1))/float64(f.TotalRuns))
		}

		confidence := 1.0 - 1.0/(1.0+float64(f.TotalRuns)/10.0)

		board = append(board, Recommendation{
			SpeciesID:  f.SpeciesID,
			Species:    *species,
			Fitness:    f,
			Confidence: confidence,
		})
	}

	// Sort by UCB score descending
	for i := 0; i < len(board); i++ {
		for j := i + 1; j < len(board); j++ {
			if board[j].Fitness.UCBScore > board[i].Fitness.UCBScore {
				board[i], board[j] = board[j], board[i]
			}
		}
	}

	return board, nil
}

// FormatToolPattern converts a tool sequence to a readable string.
func FormatToolPattern(tools []string) string {
	return strings.Join(tools, " → ")
}
