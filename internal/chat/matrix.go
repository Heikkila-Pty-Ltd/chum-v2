// Package chat implements bidirectional Matrix chat for CHUM v2 planning.
// It polls a Matrix room for user commands and sends push notifications.
package chat

import (
	"context"
	"fmt"
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
// Delegates to MatrixClient.
func ReadRoomMessages(ctx context.Context, cfg MatrixConfig, since string) ([]InboundMessage, string, error) {
	mc := NewMatrixClient(cfg.Homeserver, cfg.AccessToken)
	return mc.ReadMessages(ctx, cfg.RoomID, since)
}

// SendMatrixMessage sends a text message to a Matrix room using the Client-Server API.
// Kept for backward compatibility with bridge.go.
func SendMatrixMessage(ctx context.Context, cfg MatrixConfig, message string) error {
	// Delegate to the notify.MatrixSender via inline construction.
	// This function exists for bridge.go's Send method — new code should use
	// notify.ChatSender directly.
	mc := NewMatrixClient(cfg.Homeserver, cfg.AccessToken)
	_, err := mc.SendMessage(ctx, cfg.RoomID, message)
	if err != nil {
		return fmt.Errorf("send matrix message to %s: %w", cfg.RoomID, err)
	}
	return nil
}
