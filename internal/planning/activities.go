package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// BeadsClient interface for dependency injection in tests
type BeadsClient interface {
	Show(ctx context.Context, issueID string) (beads.Issue, error)
}

// LLMClient interface for dependency injection in tests
type LLMClient interface {
	Generate(ctx context.Context, prompt string, model string) (string, error)
}

// Activities contains the activities needed for planning workflows
type Activities struct {
	DAG         *dag.DAG
	BeadsClient BeadsClient
	LLMClient   LLMClient
	Logger      *slog.Logger
}

// LoadIssueActivity loads the original issue from beads
func (a *Activities) LoadIssueActivity(ctx context.Context, issueID string) (*beads.Issue, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Loading issue", "IssueID", issueID)

	issue, err := a.BeadsClient.Show(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("failed to load issue %s: %w", issueID, err)
	}

	return &issue, nil
}

// ClarifyActivity performs requirements clarification using LLM
func (a *Activities) ClarifyActivity(ctx context.Context, issue beads.Issue, llmModel string) (*ClarifyResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting clarify phase", "IssueID", issue.ID)

	prompt := fmt.Sprintf(`Analyze this issue and clarify the requirements:

Title: %s
Description: %s
Acceptance Criteria: %s

Please provide:
1. Clarified and detailed requirements
2. Any clarifying questions that need answers
3. Estimated complexity (Low/Medium/High)

Respond in JSON format:
{
  "clarified_requirements": "...",
  "questions": ["..."],
  "estimated_complexity": "..."
}`, issue.Title, issue.Description, issue.AcceptanceCriteria)

	response, err := a.LLMClient.Generate(ctx, prompt, llmModel)
	if err != nil {
		return nil, fmt.Errorf("LLM clarify failed: %w", err)
	}

	var result ClarifyResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		// Fallback parsing if JSON is malformed
		result = ClarifyResult{
			ClarifiedRequirements: response,
			EstimatedComplexity:  "Medium",
		}
	}

	return &result, nil
}

// ResearchActivity performs codebase research using LLM
func (a *Activities) ResearchActivity(ctx context.Context, issue beads.Issue, clarified ClarifyResult, workDir string, llmModel string) (*ResearchResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting research phase", "IssueID", issue.ID)

	prompt := fmt.Sprintf(`Research the codebase for implementing this requirement:

Original Issue: %s
Clarified Requirements: %s

Please analyze the codebase and provide:
1. Analysis of relevant code areas
2. List of files that need modification
3. Dependencies and integrations to consider
4. Risk assessment

Respond in JSON format:
{
  "codebase_analysis": "...",
  "relevant_files": ["..."],
  "dependencies": ["..."],
  "risk_assessment": "..."
}`, issue.Title, clarified.ClarifiedRequirements)

	response, err := a.LLMClient.Generate(ctx, prompt, llmModel)
	if err != nil {
		return nil, fmt.Errorf("LLM research failed: %w", err)
	}

	var result ResearchResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		// Fallback parsing if JSON is malformed
		result = ResearchResult{
			CodebaseAnalysis: response,
			RelevantFiles:   []string{},
			Dependencies:    []string{},
			RiskAssessment:  "Medium risk",
		}
	}

	return &result, nil
}

// SelectActivity performs approach selection using LLM
func (a *Activities) SelectActivity(ctx context.Context, issue beads.Issue, clarified ClarifyResult, research ResearchResult, llmModel string) (*SelectResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting select phase", "IssueID", issue.ID)

	prompt := fmt.Sprintf(`Based on the requirements and research, select the best implementation approach:

Requirements: %s
Codebase Analysis: %s
Risk Assessment: %s

Please provide:
1. Selected approach with detailed explanation
2. Alternative options considered
3. Rationale for the selection
4. High-level implementation plan

Respond in JSON format:
{
  "selected_approach": "...",
  "alternative_options": ["..."],
  "rationale": "...",
  "implementation_plan": "..."
}`, clarified.ClarifiedRequirements, research.CodebaseAnalysis, research.RiskAssessment)

	response, err := a.LLMClient.Generate(ctx, prompt, llmModel)
	if err != nil {
		return nil, fmt.Errorf("LLM select failed: %w", err)
	}

	var result SelectResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		// Fallback parsing if JSON is malformed
		result = SelectResult{
			SelectedApproach:     response,
			AlternativeOptions:   []string{},
			Rationale:           "Selected based on analysis",
			ImplementationPlan:   "Step-by-step implementation",
		}
	}

	return &result, nil
}

// DecomposeActivity breaks down the work into subtasks using LLM
func (a *Activities) DecomposeActivity(ctx context.Context, issue beads.Issue, selectedApproach SelectResult, llmModel string) (*DecomposeResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting decompose phase", "IssueID", issue.ID)

	prompt := fmt.Sprintf(`Decompose this work into specific subtasks:

Original Issue: %s
Selected Approach: %s
Implementation Plan: %s

Create a list of subtasks that are:
- Specific and actionable
- 15-60 minutes each
- Have clear acceptance criteria
- Properly ordered with dependencies

Respond in JSON format:
{
  "subtasks": [
    {
      "title": "...",
      "description": "...",
      "acceptance_criteria": "...",
      "estimate_minutes": 30,
      "priority": 1,
      "dependencies": ["..."],
      "labels": ["..."]
    }
  ],
  "timeline": "..."
}`, issue.Title, selectedApproach.SelectedApproach, selectedApproach.ImplementationPlan)

	response, err := a.LLMClient.Generate(ctx, prompt, llmModel)
	if err != nil {
		return nil, fmt.Errorf("LLM decompose failed: %w", err)
	}

	var result DecomposeResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		// Fallback parsing if JSON is malformed - create default subtasks
		result = DecomposeResult{
			Subtasks: []SubtaskSpec{
				{
					Title:           fmt.Sprintf("Implement %s", issue.Title),
					Description:     issue.Description,
					AcceptanceCriteria: issue.AcceptanceCriteria,
					EstimateMinutes: 30,
					Priority:        1,
					Labels:          []string{"generated"},
				},
			},
			Timeline: "1-2 hours",
		}
	}

	return &result, nil
}

// CreateSubtasksActivity creates the subtasks in the DAG
func (a *Activities) CreateSubtasksActivity(ctx context.Context, subtasks []SubtaskSpec, project string, parentID string) ([]string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Creating subtasks", "Count", len(subtasks), "Project", project)

	var createdIDs []string
	for i, spec := range subtasks {
		task := dag.Task{
			Title:           spec.Title,
			Description:     spec.Description,
			Status:          "pending",
			Priority:        spec.Priority,
			Type:            "subtask",
			Labels:          spec.Labels,
			EstimateMinutes: spec.EstimateMinutes,
			ParentID:        parentID,
			Acceptance:      spec.AcceptanceCriteria,
			Project:         project,
		}

		taskID, err := a.DAG.CreateTask(ctx, task)
		if err != nil {
			return nil, fmt.Errorf("failed to create subtask %d: %w", i, err)
		}

		createdIDs = append(createdIDs, taskID)
		logger.Info("Created subtask", "TaskID", taskID, "Title", spec.Title)
	}

	return createdIDs, nil
}

// RecordPlanningDecisionActivity records the final planning decision
func (a *Activities) RecordPlanningDecisionActivity(ctx context.Context, req PlanningRequest, state PlanningState) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Recording planning decision", "IssueID", req.IssueID)

	// Create a comprehensive decision record
	decisionParts := []string{
		fmt.Sprintf("PLANNING DECISION for Issue %s", req.IssueID),
		fmt.Sprintf("Original Title: %s", state.OriginalIssue.Title),
		"",
		"PHASES COMPLETED:",
	}

	if state.ClarifyResult != nil {
		decisionParts = append(decisionParts, fmt.Sprintf("✓ CLARIFY: %s", state.ClarifyResult.EstimatedComplexity))
	}
	if state.ResearchResult != nil {
		decisionParts = append(decisionParts, fmt.Sprintf("✓ RESEARCH: Analyzed %d files", len(state.ResearchResult.RelevantFiles)))
	}
	if state.SelectResult != nil {
		decisionParts = append(decisionParts, "✓ SELECT: Approach selected")
	}
	if state.GreenlightResult != nil {
		status := "REJECTED"
		if state.GreenlightResult.Approved {
			status = "APPROVED"
		}
		decisionParts = append(decisionParts, fmt.Sprintf("✓ GREENLIGHT: %s", status))
	}
	if state.DecomposeResult != nil {
		decisionParts = append(decisionParts, fmt.Sprintf("✓ DECOMPOSE: Created %d subtasks", len(state.DecomposeResult.Subtasks)))
	}
	if state.ApprovalResult != nil {
		status := "REJECTED"
		if state.ApprovalResult.Approved {
			status = "APPROVED"
		}
		decisionParts = append(decisionParts, fmt.Sprintf("✓ APPROVE: %s", status))
	}
	if state.HandoffResult != nil {
		decisionParts = append(decisionParts, fmt.Sprintf("✓ HANDOFF: Tasks created: %s", strings.Join(state.HandoffResult.CreatedTaskIDs, ", ")))
	}

	decisionRecord := strings.Join(decisionParts, "\n")

	// For now, just log the decision record
	// In a real implementation, this might save to a database or external system
	logger.Info("Planning decision recorded", "Decision", decisionRecord)

	return nil
}