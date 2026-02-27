package engine

import (
	"context"
	"strings"

	"go.temporal.io/sdk/activity"
)

// NotifyActivity sends a summary notification via the configured ChatSender.
// If ChatSender is a NullSender, the message is silently dropped.
func (a *Activities) NotifyActivity(ctx context.Context, message string) error {
	logger := activity.GetLogger(ctx)

	msg := strings.TrimSpace(message)
	if msg == "" {
		return nil
	}

	if a.ChatSend == nil {
		logger.Warn("NotifyActivity skipped: no ChatSender configured")
		return nil
	}

	roomID := ""
	if a.Config != nil {
		roomID = a.Config.General.MatrixRoomID
	}
	return a.ChatSend.Send(ctx, roomID, msg)
}
