package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
)

// DialTemporal creates a Temporal client from config.
// Shared between worker startup and CLI commands.
func DialTemporal(cfg *config.Config, logger *slog.Logger) (client.Client, error) {
	opts := client.Options{
		HostPort:  cfg.General.TemporalHostPort,
		Namespace: cfg.General.TemporalNamespace,
	}
	if logger != nil {
		opts.Logger = slogAdapter{logger}
	}
	c, err := client.Dial(opts)
	if err != nil {
		return nil, fmt.Errorf("connect to temporal: %w", err)
	}
	return c, nil
}

// ScheduleSpec describes a Temporal schedule to register idempotently.
type ScheduleSpec struct {
	ID        string
	Interval  time.Duration
	Workflow  interface{}
	Args      []interface{}
	TaskQueue string
	RunID     string // workflow ID for each run
}

// RegisterSchedule creates a Temporal schedule idempotently.
// If the schedule already exists, it logs and returns nil.
func RegisterSchedule(c client.Client, spec ScheduleSpec, logger *slog.Logger) error {
	schedClient := c.ScheduleClient()
	_, err := schedClient.Create(context.Background(), client.ScheduleOptions{
		ID: spec.ID,
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{
				{Every: spec.Interval},
			},
		},
		Action: &client.ScheduleWorkflowAction{
			Workflow:  spec.Workflow,
			Args:      spec.Args,
			TaskQueue: spec.TaskQueue,
			ID:        spec.RunID,
		},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "already exists") ||
			strings.Contains(errMsg, "already registered") ||
			strings.Contains(errMsg, "AlreadyExists") {
			logger.Info("Schedule already exists", "id", spec.ID, "interval", spec.Interval)
			return nil
		}
		return fmt.Errorf("create schedule %s: %w", spec.ID, err)
	}

	logger.Info("Schedule registered", "id", spec.ID, "interval", spec.Interval)
	return nil
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
