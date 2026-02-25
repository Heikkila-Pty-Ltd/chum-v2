package engine

import (
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/Heikkila-Pty-Ltd/chum/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum/internal/dag"
)

// StartWorker connects to Temporal and starts the CHUM v2 worker.
func StartWorker(cfg *config.Config, d *dag.DAG, logger *slog.Logger) error {
	c, err := client.Dial(client.Options{
		HostPort:  cfg.General.TemporalHostPort,
		Namespace: cfg.General.TemporalNamespace,
		Logger:    slogAdapter{logger},
	})
	if err != nil {
		return fmt.Errorf("connect to temporal: %w", err)
	}

	w := worker.New(c, cfg.General.TaskQueue, worker.Options{})

	// Register workflows
	w.RegisterWorkflow(AgentWorkflow)
	w.RegisterWorkflow(DispatcherWorkflow)

	// Register activities
	a := &Activities{
		DAG:    d,
		Config: cfg,
		Logger: logger,
	}
	w.RegisterActivity(a)

	da := &DispatchActivities{
		DAG:    d,
		Config: cfg,
	}
	w.RegisterActivity(da)

	logger.Info("Starting CHUM v2 worker",
		"task_queue", cfg.General.TaskQueue,
		"namespace", cfg.General.TemporalNamespace,
	)
	return w.Run(worker.InterruptCh())
}

// slogAdapter wraps slog.Logger to satisfy Temporal's log.Logger interface.
type slogAdapter struct {
	l *slog.Logger
}

func (s slogAdapter) Debug(msg string, keysAndValues ...interface{}) {
	s.l.Debug(msg, keysAndValues...)
}
func (s slogAdapter) Info(msg string, keysAndValues ...interface{}) {
	s.l.Info(msg, keysAndValues...)
}
func (s slogAdapter) Warn(msg string, keysAndValues ...interface{}) {
	s.l.Warn(msg, keysAndValues...)
}
func (s slogAdapter) Error(msg string, keysAndValues ...interface{}) {
	s.l.Error(msg, keysAndValues...)
}
