package jarvis

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/engine"
)

// JarvisTaskRequest extends TaskRequest with Jarvis-specific fields.
type JarvisTaskRequest struct {
	TaskID      string        `json:"task_id"`
	Project     string        `json:"project"`
	Prompt      string        `json:"prompt"`
	WorkDir     string        `json:"work_dir"`
	Agent       string        `json:"agent"`
	ExecTimeout time.Duration `json:"exec_timeout,omitempty"`
	Source      string        `json:"source,omitempty"`
	Callback    string        `json:"callback,omitempty"`
}

// JarvisAgentWorkflow wraps the standard AgentWorkflow with Jarvis-specific
// pre/post processing. It records the decision in Jarvis's knowledge graph
// and optionally calls back with results.
func JarvisAgentWorkflow(ctx workflow.Context, req JarvisTaskRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("JarvisAgentWorkflow started",
		"TaskID", req.TaskID,
		"Project", req.Project,
		"Source", req.Source,
	)

	startTime := workflow.Now(ctx)

	// Convert to standard TaskRequest and delegate to AgentWorkflow.
	taskReq := engine.TaskRequest{
		TaskID:      req.TaskID,
		Project:     req.Project,
		Prompt:      req.Prompt,
		WorkDir:     req.WorkDir,
		Agent:       req.Agent,
		ExecTimeout: req.ExecTimeout,
	}

	// Run the standard agent pipeline.
	childOpts := workflow.ChildWorkflowOptions{
		WorkflowID:               "chum-agent-" + req.TaskID,
		WorkflowExecutionTimeout: 2 * time.Hour,
	}
	childCtx := workflow.WithChildOptions(ctx, childOpts)

	err := workflow.ExecuteChildWorkflow(childCtx, engine.AgentWorkflow, taskReq).Get(ctx, nil)

	elapsed := workflow.Now(ctx).Sub(startTime)

	// Record completion via activity (callback, logging, etc).
	if req.Callback != "" {
		actOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
		}
		actCtx := workflow.WithActivityOptions(ctx, actOpts)

		result := CallbackPayload{
			TaskID:   req.TaskID,
			Project:  req.Project,
			Source:   req.Source,
			Duration: elapsed,
			Success:  err == nil,
		}
		if err != nil {
			result.Error = err.Error()
		}

		// Fire-and-forget callback — don't fail the workflow if callback fails.
		_ = workflow.ExecuteActivity(actCtx, CallbackActivity, req.Callback, result).Get(ctx, nil)
	}

	if err != nil {
		logger.Error("JarvisAgentWorkflow failed",
			"TaskID", req.TaskID,
			"Duration", elapsed,
			"Error", err,
		)
		return err
	}

	logger.Info("JarvisAgentWorkflow completed",
		"TaskID", req.TaskID,
		"Duration", elapsed,
	)
	return nil
}
