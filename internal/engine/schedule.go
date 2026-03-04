package engine

import (
	"context"
	"errors"
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
	Paused    bool   // Legacy fallback: keep paused instead of unpausing on startup
	PauseDB   GlobalPauseReader
}

// DispatcherScheduleID is the canonical Temporal schedule ID for dispatch ticks.
const DispatcherScheduleID = "chum-v2-dispatcher"

// GlobalPauseReader reports whether the system is globally paused.
// IsGlobalPauseSet returns the DB value and whether a row exists;
// callers fall back to config when isSet is false.
type GlobalPauseReader interface {
	IsGlobalPauseSet(ctx context.Context) (paused bool, isSet bool, err error)
}

// RegisterSchedule creates a Temporal schedule idempotently.
// If the schedule already exists, it logs and returns nil.
func RegisterSchedule(ctx context.Context, c client.Client, spec ScheduleSpec, logger *slog.Logger) error {
	schedClient := c.ScheduleClient()
	handle, err := schedClient.Create(ctx, client.ScheduleOptions{
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
			logger.Info("Schedule already exists, updating", "id", spec.ID, "interval", spec.Interval)
			return ensureScheduleActive(ctx, schedClient, spec, logger)
		}
		return fmt.Errorf("create schedule %s: %w", spec.ID, err)
	}

	paused, err := scheduleShouldPause(ctx, spec)
	if err != nil {
		return fmt.Errorf("resolve pause state for schedule %s: %w", spec.ID, err)
	}
	if paused {
		if handle == nil {
			handle = schedClient.GetHandle(ctx, spec.ID)
		}
		if err := handle.Pause(ctx, client.SchedulePauseOptions{
			Note: "global pause enabled on startup",
		}); err != nil {
			return fmt.Errorf("pause schedule %s: %w", spec.ID, err)
		}
		logger.Info("Schedule registered and left paused", "id", spec.ID, "interval", spec.Interval)
		return nil
	}

	logger.Info("Schedule registered", "id", spec.ID, "interval", spec.Interval)
	return nil
}

// ensureScheduleActive updates an existing schedule's configuration and
// unpauses it if necessary. Called when RegisterSchedule detects the
// schedule already exists — this is the ch-16434 fix: without this,
// paused or stale schedules would silently block dispatching.
func ensureScheduleActive(ctx context.Context, schedClient client.ScheduleClient, spec ScheduleSpec, logger *slog.Logger) error {
	handle := schedClient.GetHandle(ctx, spec.ID)
	var activationErrs []error

	// Update the schedule with the current configuration (handles tick_interval changes).
	if err := handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			input.Description.Schedule.Action = &client.ScheduleWorkflowAction{
				Workflow:  spec.Workflow,
				Args:      spec.Args,
				TaskQueue: spec.TaskQueue,
				ID:        spec.RunID,
			}
			input.Description.Schedule.Spec = &client.ScheduleSpec{
				Intervals: []client.ScheduleIntervalSpec{
					{Every: spec.Interval},
				},
			}
			return &client.ScheduleUpdate{
				Schedule: &input.Description.Schedule,
			}, nil
		},
	}); err != nil {
		logger.Warn("Failed to update schedule, continuing", "id", spec.ID, "error", err)
		activationErrs = append(activationErrs, fmt.Errorf("update schedule %s: %w", spec.ID, err))
	} else {
		logger.Info("Schedule updated", "id", spec.ID, "interval", spec.Interval)
	}

	paused, err := scheduleShouldPause(ctx, spec)
	if err != nil {
		// Fail-closed: if we can't determine pause state, don't unpause/trigger.
		return fmt.Errorf("resolve pause state for schedule %s: %w", spec.ID, err)
	}
	if paused {
		// Global pause: keep the schedule paused, do not trigger.
		if err := handle.Pause(ctx, client.SchedulePauseOptions{
			Note: "global pause enabled on startup",
		}); err != nil {
			logger.Warn("Failed to pause schedule", "id", spec.ID, "error", err)
			activationErrs = append(activationErrs, fmt.Errorf("pause schedule %s: %w", spec.ID, err))
		} else {
			logger.Info("Schedule kept paused due to global pause", "id", spec.ID)
		}
	} else {
		// Unpause — harmless if already active, critical if paused.
		if err := handle.Unpause(ctx, client.ScheduleUnpauseOptions{
			Note: "unpaused on chum serve startup",
		}); err != nil {
			logger.Warn("Failed to unpause schedule", "id", spec.ID, "error", err)
			activationErrs = append(activationErrs, fmt.Errorf("unpause schedule %s: %w", spec.ID, err))
		}

		// Trigger an immediate run so ready tasks don't wait for the first tick.
		if err := handle.Trigger(ctx, client.ScheduleTriggerOptions{
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		}); err != nil {
			logger.Warn("Failed to trigger immediate schedule run", "id", spec.ID, "error", err)
			activationErrs = append(activationErrs, fmt.Errorf("trigger schedule %s: %w", spec.ID, err))
		} else {
			logger.Info("Triggered immediate schedule run", "id", spec.ID)
		}
	}

	if len(activationErrs) > 0 {
		return fmt.Errorf("ensure schedule %s active: %w", spec.ID, errors.Join(activationErrs...))
	}

	return nil
}

func scheduleShouldPause(ctx context.Context, spec ScheduleSpec) (bool, error) {
	if spec.PauseDB != nil {
		paused, isSet, err := spec.PauseDB.IsGlobalPauseSet(ctx)
		if err != nil {
			return false, err
		}
		if isSet {
			return paused, nil // DB value overrides config
		}
	}
	return spec.Paused, nil
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
