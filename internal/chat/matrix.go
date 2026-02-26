// Package chat implements bidirectional Matrix chat for CHUM v2 planning.
// It polls a Matrix room for user commands and sends push notifications.
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// InboundMessage is a text message received from a Matrix room.
type InboundMessage struct {
	ID        string    `json:"id"`
	Room      string    `json:"room"`
	Sender    string    `json:"sender"`
	Body      string    `json:"body"`
	Timestamp time.Time `json:"timestamp"`
}

// MatrixConfig holds credentials for a Matrix homeserver.
type MatrixConfig struct {
	Homeserver  string
	RoomID      string
	AccessToken string
}

// ReadRoomMessages reads recent text messages from a Matrix room.
// The since token enables pagination — pass empty string for the first call,
// then use the returned token for subsequent calls.
func ReadRoomMessages(ctx context.Context, cfg MatrixConfig, since string) ([]InboundMessage, string, error) {
	endpoint := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/messages?dir=f&limit=20",
		strings.TrimRight(cfg.Homeserver, "/"),
		url.PathEscape(cfg.RoomID),
	)
	if since != "" {
		endpoint += "&from=" + url.QueryEscape(since)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("create messages request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("read messages: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, "", fmt.Errorf("read messages response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("matrix messages: status %d: %s", resp.StatusCode, truncate(string(body), 200))
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
			Room:      cfg.RoomID,
			Sender:    evt.Sender,
			Body:      evt.Content.Body,
			Timestamp: time.Unix(evt.OriginServer/1000, 0),
		})
	}

	return messages, result.End, nil
}

// resolveMatrixUserID calls the Matrix /whoami endpoint to discover the
// authenticated user's ID. Used to filter out the bot's own messages.
func resolveMatrixUserID(ctx context.Context, cfg MatrixConfig) (string, error) {
	endpoint := fmt.Sprintf("%s/_matrix/client/v3/account/whoami",
		strings.TrimRight(cfg.Homeserver, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("create whoami request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("whoami request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("read whoami response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("whoami HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var result struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse whoami: %w", err)
	}
	return result.UserID, nil
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
