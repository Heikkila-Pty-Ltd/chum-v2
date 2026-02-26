package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// NotifyActivity sends a summary notification to Matrix when configured.
// If Matrix config is missing, it logs and returns nil (non-blocking behavior).
func (a *Activities) NotifyActivity(ctx context.Context, message string) error {
	logger := activity.GetLogger(ctx)
	if a.Config == nil {
		logger.Warn("NotifyActivity skipped: config missing")
		return nil
	}

	msg := strings.TrimSpace(message)
	if msg == "" {
		return nil
	}

	g := a.Config.General
	if strings.TrimSpace(g.MatrixWebhookURL) != "" {
		return postMatrixWebhook(ctx, g.MatrixWebhookURL, msg)
	}

	if strings.TrimSpace(g.MatrixHomeserver) == "" ||
		strings.TrimSpace(g.MatrixRoomID) == "" ||
		strings.TrimSpace(g.MatrixAccessToken) == "" {
		logger.Warn("NotifyActivity skipped: matrix config not set")
		return nil
	}

	return postMatrixMessage(ctx, g.MatrixHomeserver, g.MatrixRoomID, g.MatrixAccessToken, msg)
}

func postMatrixWebhook(ctx context.Context, webhookURL, message string) error {
	payload := map[string]string{"text": message}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create matrix webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send matrix webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("matrix webhook HTTP %d", resp.StatusCode)
	}
	return nil
}

func postMatrixMessage(ctx context.Context, homeserver, roomID, accessToken, message string) error {
	txnID := fmt.Sprintf("chum-%d", time.Now().UnixNano())
	path := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		strings.TrimRight(homeserver, "/"),
		url.PathEscape(roomID),
		txnID,
	)
	payload := map[string]string{
		"msgtype": "m.text",
		"body":    message,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create matrix message request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send matrix message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("matrix send HTTP %d", resp.StatusCode)
	}
	return nil
}
