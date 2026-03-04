package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/stretchr/testify/mock"
)

func TestRegisterScheduleCreatesSchedule(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := testScheduleLogger()
	spec := testScheduleSpec()

	c := temporalmocks.NewClient(t)
	schedClient := temporalmocks.NewScheduleClient(t)

	c.On("ScheduleClient").Return(schedClient).Once()
	schedClient.On("Create", mock.Anything, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
		action, ok := opts.Action.(*client.ScheduleWorkflowAction)
		if !ok {
			return false
		}
		return opts.ID == spec.ID &&
			len(opts.Spec.Intervals) == 1 &&
			opts.Spec.Intervals[0].Every == spec.Interval &&
			opts.Overlap == enumspb.SCHEDULE_OVERLAP_POLICY_SKIP &&
			action.Workflow == spec.Workflow &&
			reflect.DeepEqual(action.Args, spec.Args) &&
			action.TaskQueue == spec.TaskQueue &&
			action.ID == spec.RunID
	})).Return((client.ScheduleHandle)(nil), nil).Once()

	err := RegisterSchedule(ctx, c, spec, logger)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRegisterScheduleCreatesPausedWhenGlobalPauseSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := testScheduleLogger()
	spec := testScheduleSpec()
	spec.PauseDB = staticPauseReader{paused: true}

	c := temporalmocks.NewClient(t)
	schedClient := temporalmocks.NewScheduleClient(t)
	handle := temporalmocks.NewScheduleHandle(t)

	c.On("ScheduleClient").Return(schedClient).Once()
	schedClient.On("Create", mock.Anything, mock.Anything).Return((client.ScheduleHandle)(nil), nil).Once()
	schedClient.On("GetHandle", mock.Anything, spec.ID).Return(handle).Once()
	handle.On("Pause", mock.Anything, client.SchedulePauseOptions{
		Note: "global pause enabled on startup",
	}).Return(nil).Once()

	err := RegisterSchedule(ctx, c, spec, logger)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRegisterScheduleAlreadyExistsReactivationSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := testScheduleLogger()
	spec := testScheduleSpec()

	c := temporalmocks.NewClient(t)
	schedClient := temporalmocks.NewScheduleClient(t)
	handle := temporalmocks.NewScheduleHandle(t)

	c.On("ScheduleClient").Return(schedClient).Once()
	schedClient.On("Create", mock.Anything, mock.Anything).
		Return((client.ScheduleHandle)(nil), errors.New("AlreadyExists: schedule already exists")).
		Once()
	schedClient.On("GetHandle", mock.Anything, spec.ID).Return(handle).Once()
	handle.On("Update", mock.Anything, mock.MatchedBy(matchScheduleUpdateOptions(spec))).Return(nil).Once()
	handle.On("Unpause", mock.Anything, client.ScheduleUnpauseOptions{
		Note: "unpaused on chum serve startup",
	}).Return(nil).Once()
	handle.On("Trigger", mock.Anything, client.ScheduleTriggerOptions{
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	}).Return(nil).Once()

	err := RegisterSchedule(ctx, c, spec, logger)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRegisterScheduleAlreadyExistsPausedKeepsPaused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := testScheduleLogger()
	spec := testScheduleSpec()
	spec.Paused = true

	c := temporalmocks.NewClient(t)
	schedClient := temporalmocks.NewScheduleClient(t)
	handle := temporalmocks.NewScheduleHandle(t)

	c.On("ScheduleClient").Return(schedClient).Once()
	schedClient.On("Create", mock.Anything, mock.Anything).
		Return((client.ScheduleHandle)(nil), errors.New("AlreadyExists: schedule already exists")).
		Once()
	schedClient.On("GetHandle", mock.Anything, spec.ID).Return(handle).Once()
	handle.On("Update", mock.Anything, mock.MatchedBy(matchScheduleUpdateOptions(spec))).Return(nil).Once()
	handle.On("Pause", mock.Anything, client.SchedulePauseOptions{
		Note: "global pause enabled on startup",
	}).Return(nil).Once()

	err := RegisterSchedule(ctx, c, spec, logger)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Verify Unpause and Trigger were NOT called
	handle.AssertNotCalled(t, "Unpause", mock.Anything, mock.Anything)
	handle.AssertNotCalled(t, "Trigger", mock.Anything, mock.Anything)
}

func TestRegisterScheduleAlreadyExistsPausedByDBKeepsPaused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := testScheduleLogger()
	spec := testScheduleSpec()
	spec.PauseDB = staticPauseReader{paused: true}

	c := temporalmocks.NewClient(t)
	schedClient := temporalmocks.NewScheduleClient(t)
	handle := temporalmocks.NewScheduleHandle(t)

	c.On("ScheduleClient").Return(schedClient).Once()
	schedClient.On("Create", mock.Anything, mock.Anything).
		Return((client.ScheduleHandle)(nil), errors.New("AlreadyExists: schedule already exists")).
		Once()
	schedClient.On("GetHandle", mock.Anything, spec.ID).Return(handle).Once()
	handle.On("Update", mock.Anything, mock.MatchedBy(matchScheduleUpdateOptions(spec))).Return(nil).Once()
	handle.On("Pause", mock.Anything, client.SchedulePauseOptions{
		Note: "global pause enabled on startup",
	}).Return(nil).Once()

	err := RegisterSchedule(ctx, c, spec, logger)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	handle.AssertNotCalled(t, "Unpause", mock.Anything, mock.Anything)
	handle.AssertNotCalled(t, "Trigger", mock.Anything, mock.Anything)
}

func TestRegisterScheduleAlreadyExistsReactivationErrorsReturned(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := testScheduleLogger()
	spec := testScheduleSpec()

	c := temporalmocks.NewClient(t)
	schedClient := temporalmocks.NewScheduleClient(t)
	handle := temporalmocks.NewScheduleHandle(t)

	c.On("ScheduleClient").Return(schedClient).Once()
	schedClient.On("Create", mock.Anything, mock.Anything).
		Return((client.ScheduleHandle)(nil), errors.New("AlreadyExists: schedule already exists")).
		Once()
	schedClient.On("GetHandle", mock.Anything, spec.ID).Return(handle).Once()
	handle.On("Update", mock.Anything, mock.MatchedBy(matchScheduleUpdateOptions(spec))).Return(errors.New("update failed")).Once()
	handle.On("Unpause", mock.Anything, client.ScheduleUnpauseOptions{
		Note: "unpaused on chum serve startup",
	}).Return(errors.New("unpause failed")).Once()
	handle.On("Trigger", mock.Anything, client.ScheduleTriggerOptions{
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	}).Return(errors.New("trigger failed")).Once()

	err := RegisterSchedule(ctx, c, spec, logger)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"ensure schedule " + spec.ID + " active",
		"update schedule " + spec.ID,
		"unpause schedule " + spec.ID,
		"trigger schedule " + spec.ID,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got %q", want, msg)
		}
	}
}

func matchScheduleUpdateOptions(spec ScheduleSpec) func(client.ScheduleUpdateOptions) bool {
	return func(opts client.ScheduleUpdateOptions) bool {
		if opts.DoUpdate == nil {
			return false
		}

		update, err := opts.DoUpdate(client.ScheduleUpdateInput{
			Description: client.ScheduleDescription{
				Schedule: client.Schedule{
					Action: &client.ScheduleWorkflowAction{},
					Spec:   &client.ScheduleSpec{},
				},
			},
		})
		if err != nil || update == nil || update.Schedule == nil {
			return false
		}

		action, ok := update.Schedule.Action.(*client.ScheduleWorkflowAction)
		if !ok {
			return false
		}
		if action.Workflow != spec.Workflow ||
			!reflect.DeepEqual(action.Args, spec.Args) ||
			action.TaskQueue != spec.TaskQueue ||
			action.ID != spec.RunID {
			return false
		}

		if update.Schedule.Spec == nil || len(update.Schedule.Spec.Intervals) != 1 {
			return false
		}
		return update.Schedule.Spec.Intervals[0].Every == spec.Interval
	}
}

func testScheduleSpec() ScheduleSpec {
	return ScheduleSpec{
		ID:        "chum-v2-dispatcher",
		Interval:  5 * time.Minute,
		Workflow:  "dispatcher-workflow",
		Args:      []interface{}{"arg"},
		TaskQueue: "default",
		RunID:     "chum-v2-dispatcher-run",
	}
}

func testScheduleLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type staticPauseReader struct {
	paused bool
	err    error
}

func (s staticPauseReader) IsGlobalPaused(context.Context) (bool, error) {
	return s.paused, s.err
}
