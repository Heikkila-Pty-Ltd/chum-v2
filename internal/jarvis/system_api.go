package jarvis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/engine"
)

type systemControlRequest struct {
	Reason string `json:"reason"`
}

func (a *API) handleSystemPause(w http.ResponseWriter, r *http.Request) {
	if a.DAG == nil {
		a.jsonError(w, "dag store unavailable", http.StatusServiceUnavailable)
		return
	}

	reason, err := decodeSystemReason(r, "paused via API")
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Verify Temporal is reachable before persisting DB state to avoid
	// writing a hidden pause when the schedule call will inevitably fail.
	if _, err := a.dispatcherScheduleHandle(r.Context()); err != nil {
		a.jsonError(w, fmt.Sprintf("temporal unavailable: %v", err), http.StatusServiceUnavailable)
		return
	}

	// Snapshot prior DB state so we can restore on rollback.
	prevPaused, prevSet, err := a.DAG.IsGlobalPauseSet(r.Context())
	if err != nil {
		a.jsonError(w, fmt.Sprintf("read pause state: %v", err), http.StatusInternalServerError)
		return
	}

	if err := a.DAG.SetGlobalPaused(r.Context(), true); err != nil {
		a.jsonError(w, fmt.Sprintf("set global pause: %v", err), http.StatusInternalServerError)
		return
	}
	if err := a.pauseDispatcherSchedule(r.Context(), reason); err != nil {
		// Restore prior DB state since schedule pause failed.
		if prevSet {
			_ = a.DAG.SetGlobalPaused(r.Context(), prevPaused)
		}
		a.jsonError(w, fmt.Sprintf("schedule pause failed (DB state rolled back): %v", err), http.StatusInternalServerError)
		return
	}

	a.jsonOK(w, map[string]any{
		"paused":      true,
		"schedule_id": engine.DispatcherScheduleID,
		"reason":      reason,
	})
}

func (a *API) handleSystemResume(w http.ResponseWriter, r *http.Request) {
	if a.DAG == nil {
		a.jsonError(w, "dag store unavailable", http.StatusServiceUnavailable)
		return
	}

	reason, err := decodeSystemReason(r, "resumed via API")
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := a.unpauseDispatcherSchedule(r.Context(), reason); err != nil {
		a.jsonError(w, fmt.Sprintf("resume schedule: %v", err), http.StatusInternalServerError)
		return
	}
	if err := a.DAG.SetGlobalPaused(r.Context(), false); err != nil {
		_ = a.pauseDispatcherSchedule(r.Context(), "re-paused after resume state write failure")
		a.jsonError(w, fmt.Sprintf("clear global pause: %v", err), http.StatusInternalServerError)
		return
	}

	triggered := true
	if err := a.triggerDispatcherSchedule(r.Context()); err != nil {
		a.Logger.Warn("Failed to trigger dispatcher schedule after resume", "error", err)
		triggered = false
	}

	a.jsonOK(w, map[string]any{
		"paused":      false,
		"schedule_id": engine.DispatcherScheduleID,
		"reason":      reason,
		"triggered":   triggered,
	})
}

func (a *API) dispatcherScheduleHandle(ctx context.Context) (client.ScheduleHandle, error) {
	if a.Engine == nil || a.Engine.temporal == nil {
		return nil, fmt.Errorf("temporal client unavailable")
	}
	return a.Engine.temporal.ScheduleClient().GetHandle(ctx, engine.DispatcherScheduleID), nil
}

func (a *API) pauseDispatcherSchedule(ctx context.Context, reason string) error {
	handle, err := a.dispatcherScheduleHandle(ctx)
	if err != nil {
		return err
	}
	return handle.Pause(ctx, client.SchedulePauseOptions{Note: reason})
}

func (a *API) unpauseDispatcherSchedule(ctx context.Context, reason string) error {
	handle, err := a.dispatcherScheduleHandle(ctx)
	if err != nil {
		return err
	}
	return handle.Unpause(ctx, client.ScheduleUnpauseOptions{Note: reason})
}

func (a *API) triggerDispatcherSchedule(ctx context.Context) error {
	handle, err := a.dispatcherScheduleHandle(ctx)
	if err != nil {
		return err
	}
	return handle.Trigger(ctx, client.ScheduleTriggerOptions{
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
}

func decodeSystemReason(r *http.Request, fallback string) (string, error) {
	var req systemControlRequest
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("invalid request body")
		}
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = fallback
	}
	return reason, nil
}
