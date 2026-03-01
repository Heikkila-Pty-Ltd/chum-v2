package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// MatrixClient wraps HTTP operations against a Matrix homeserver.
type MatrixClient struct {
	homeserver  string
	accessToken string
	client      *http.Client
	txnCounter  uint64
}

// NewMatrixClient creates a client for the given homeserver and access token.
func NewMatrixClient(homeserver, accessToken string) *MatrixClient {
	return &MatrixClient{
		homeserver:  strings.TrimRight(homeserver, "/"),
		accessToken: accessToken,
		client:      &http.Client{Timeout: 10 * time.Second},
	}
}

// ReadMessages reads recent text messages from a Matrix room.
// The since token enables pagination — pass empty string for the first call.
func (m *MatrixClient) ReadMessages(ctx context.Context, roomID, since string) ([]InboundMessage, string, error) {
	endpoint := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/messages?dir=f&limit=20",
		m.homeserver, url.PathEscape(roomID))
	if since != "" {
		endpoint += "&from=" + url.QueryEscape(since)
	}

	body, err := m.doGet(ctx, endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("read messages: %w", err)
	}

	var result struct {
		Chunk []struct {
			EventID string `json:"event_id"`
			Sender  string `json:"sender"`
			Content struct {
				MsgType string `json:"msgtype"`
				Body    string `json:"body"`
			} `json:"content"`
			Type         string `json:"type"`
			OriginServer int64  `json:"origin_server_ts"`
		} `json:"chunk"`
		End string `json:"end"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "", fmt.Errorf("parse messages: %w", err)
	}

	messages := make([]InboundMessage, 0, len(result.Chunk))
	for _, evt := range result.Chunk {
		if evt.Type != "m.room.message" || evt.Content.MsgType != "m.text" {
			continue
		}
		messages = append(messages, InboundMessage{
			ID:        evt.EventID,
			Room:      roomID,
			Sender:    evt.Sender,
			Body:      evt.Content.Body,
			Timestamp: time.Unix(evt.OriginServer/1000, 0),
		})
	}
	return messages, result.End, nil
}

// WhoAmI resolves the authenticated user's Matrix ID.
func (m *MatrixClient) WhoAmI(ctx context.Context) (string, error) {
	endpoint := fmt.Sprintf("%s/_matrix/client/v3/account/whoami", m.homeserver)

	body, err := m.doGet(ctx, endpoint)
	if err != nil {
		return "", fmt.Errorf("whoami: %w", err)
	}

	var result struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse whoami: %w", err)
	}
	return result.UserID, nil
}

// doGet performs an authenticated GET request and returns the response body.
func (m *MatrixClient) doGet(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.accessToken)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, types.Truncate(string(body), 200))
	}
	return body, nil
}

// SendMessage sends a text message to a Matrix room.
func (m *MatrixClient) SendMessage(ctx context.Context, roomID, message string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", nil
	}

	txnID := fmt.Sprintf("chum-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&m.txnCounter, 1))
	path := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		m.homeserver, url.PathEscape(roomID), txnID)

	payload := map[string]string{
		"msgtype": "m.text",
		"body":    message,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal matrix payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, path, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create matrix message request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send matrix message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("matrix send HTTP %d", resp.StatusCode)
	}
	return txnID, nil
}
