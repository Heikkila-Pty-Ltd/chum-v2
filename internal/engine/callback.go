package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// callbackClient is a dedicated HTTP client for callback delivery with a
// sensible timeout so a hanging endpoint cannot block the activity worker.
var callbackClient = &http.Client{Timeout: 30 * time.Second}

// validateCallbackURL checks that the URL is a valid HTTP or HTTPS URL.
// It rejects schemes like file://, ftp://, etc. that have no business being
// used as callback endpoints.
func validateCallbackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid callback URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("unsupported callback URL scheme %q: only http and https are allowed", u.Scheme)
	}
}

// CallbackInput is the input to CallbackActivity.
type CallbackInput struct {
	URL           string      `json:"url"`
	Token         string      `json:"token,omitempty"` // Bearer token for callback auth
	ExternalRef   string      `json:"external_ref"`    // Kaikki source item ID
	TaskID        string      `json:"task_id"`
	ExecutionMode string      `json:"execution_mode,omitempty"` // "code_change", "research", "command"
	Detail        CloseDetail `json:"detail"`
}

// CallbackActivity POSTs task results to an external callback URL (e.g. Kaikki webhook).
// Retries up to 3 times with exponential backoff on transient failures.
func (a *Activities) CallbackActivity(ctx context.Context, input CallbackInput) error {
	logger := activity.GetLogger(ctx)

	url := strings.TrimSpace(input.URL)
	if url == "" {
		return nil // no callback configured — skip silently
	}

	if err := validateCallbackURL(url); err != nil {
		logger.Error("Invalid callback URL, skipping", "url", url, "error", err)
		return nil
	}

	// Delivery mode: research/command results update the source item,
	// code_change results create a new KNOWLEDGE item.
	delivery := "new_item"
	if input.ExecutionMode == "research" || input.ExecutionMode == "command" {
		delivery = "update_source"
	}

	payload := map[string]interface{}{
		"sourceItemId": input.ExternalRef,
		"resultType":   mapResultType(input.Detail),
		"title":        callbackTitle(input.Detail),
		"body":         callbackBody(input.Detail),
		"workflowId":   input.TaskID,
		"status":       mapCallbackStatus(input.Detail.Reason),
		"delivery":     delivery,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal callback payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build callback request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if input.Token != "" {
			req.Header.Set("Authorization", "Bearer "+input.Token)
		}

		resp, err := callbackClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("callback POST failed (attempt %d): %w", attempt+1, err)
			logger.Warn("Callback delivery failed, retrying", "attempt", attempt+1, "error", err)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			logger.Info("Callback delivered", "url", url, "task_id", input.TaskID, "status", resp.StatusCode)
			return nil
		}

		lastErr = fmt.Errorf("callback returned %d (attempt %d)", resp.StatusCode, attempt+1)
		logger.Warn("Callback delivery got non-2xx, retrying", "attempt", attempt+1, "status", resp.StatusCode)
	}

	// All retries exhausted — log but don't fail the workflow
	logger.Error("Callback delivery failed after 3 attempts", "url", url, "task_id", input.TaskID, "error", lastErr)
	return nil
}

// mapResultType maps CHUM close detail to Kaikki's resultType enum.
func mapResultType(d CloseDetail) string {
	switch d.Reason {
	case CloseCompleted:
		if d.PRNumber > 0 {
			return "code_change"
		}
		return "research"
	case CloseDoDFailed:
		return "report"
	case CloseNeedsReview:
		return "report"
	case CloseFailed:
		return "error"
	case CloseDecomposed:
		return "report"
	default:
		return "error"
	}
}

// mapCallbackStatus maps CHUM close reason to Kaikki's status enum.
func mapCallbackStatus(reason CloseReason) string {
	switch reason {
	case CloseCompleted:
		return "success"
	case CloseDoDFailed, CloseNeedsReview, CloseDecomposed:
		return "partial"
	default:
		return "failed"
	}
}

// callbackTitle builds a human-readable title for the Kaikki result item.
func callbackTitle(d CloseDetail) string {
	if d.Summary != "" {
		return d.Summary
	}
	switch d.Reason {
	case CloseCompleted:
		if d.PRNumber > 0 {
			return fmt.Sprintf("CHUM completed: PR #%d", d.PRNumber)
		}
		return "CHUM task completed"
	case CloseDoDFailed:
		return "CHUM task: DoD check failed"
	case CloseFailed:
		return "CHUM task failed"
	case CloseNeedsReview:
		return "CHUM task needs review"
	case CloseDecomposed:
		return "CHUM task decomposed into subtasks"
	default:
		return "CHUM task result"
	}
}

// callbackBody builds markdown body for the Kaikki result item.
func callbackBody(d CloseDetail) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("**Status:** %s", d.Reason))
	if d.SubReason != "" {
		parts = append(parts, fmt.Sprintf("**Detail:** %s", d.SubReason))
	}
	if d.PRNumber > 0 {
		parts = append(parts, fmt.Sprintf("**PR:** #%d", d.PRNumber))
	}
	if d.ReviewURL != "" {
		parts = append(parts, fmt.Sprintf("**Review:** %s", d.ReviewURL))
	}
	if d.Category != "" {
		parts = append(parts, fmt.Sprintf("**Category:** %s", d.Category))
	}
	if d.Summary != "" {
		parts = append(parts, fmt.Sprintf("\n%s", d.Summary))
	}
	return strings.Join(parts, "\n")
}
