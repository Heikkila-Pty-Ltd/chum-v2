// Package notify provides a ChatSender interface for sending messages
// to chat systems (Matrix, webhooks, etc.) with null-safe guard objects.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// ChatSender sends a message to a chat room.
type ChatSender interface {
	Send(ctx context.Context, roomID, message string) error
}

// --- MatrixSender ---

// MatrixSender sends messages via the Matrix Client-Server API.
type MatrixSender struct {
	Homeserver  string
	AccessToken string
	client      *http.Client
	txnCounter  uint64
}

// NewMatrixSender creates a MatrixSender for the given homeserver.
func NewMatrixSender(homeserver, accessToken string) *MatrixSender {
	return &MatrixSender{
		Homeserver:  homeserver,
		AccessToken: accessToken,
		client:      &http.Client{Timeout: 8 * time.Second},
	}
}

func (m *MatrixSender) Send(ctx context.Context, roomID, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}

	txnID := fmt.Sprintf("chum-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&m.txnCounter, 1))
	path := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		strings.TrimRight(m.Homeserver, "/"),
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
	req.Header.Set("Authorization", "Bearer "+m.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("send matrix message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("matrix send HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- WebhookSender ---

// WebhookSender sends messages via a webhook URL (e.g. Matrix webhook bridge).
type WebhookSender struct {
	URL    string
	client *http.Client
}

// NewWebhookSender creates a WebhookSender for the given URL.
func NewWebhookSender(webhookURL string) *WebhookSender {
	return &WebhookSender{
		URL:    webhookURL,
		client: &http.Client{Timeout: 8 * time.Second},
	}
}

func (w *WebhookSender) Send(ctx context.Context, _, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}

	payload := map[string]string{"text": message}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- NullSender ---

// NullSender is a guard object that satisfies ChatSender but does nothing.
// Used when chat is not configured.
type NullSender struct {
	Logger *slog.Logger
}

func (n *NullSender) Send(_ context.Context, roomID, message string) error {
	if n.Logger != nil {
		n.Logger.Debug("Chat message dropped (no sender configured)", "room", roomID)
	}
	return nil
}
