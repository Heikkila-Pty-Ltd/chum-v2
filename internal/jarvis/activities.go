package jarvis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// CallbackPayload is sent to the callback URL when a task completes.
type CallbackPayload struct {
	TaskID   string        `json:"task_id"`
	Project  string        `json:"project"`
	Source   string        `json:"source"`
	Duration time.Duration `json:"duration"`
	Success  bool          `json:"success"`
	Error    string        `json:"error,omitempty"`
}

// CallbackActivity POSTs the result to a webhook URL.
func CallbackActivity(ctx context.Context, url string, payload CallbackPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal callback: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create callback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("callback POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("callback returned HTTP %d", resp.StatusCode)
	}
	return nil
}
