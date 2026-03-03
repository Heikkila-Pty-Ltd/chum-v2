package engine

import (
	"context"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"
)

// NotifyRequest is a structured notification event for Matrix status updates.
type NotifyRequest struct {
	Event  string            `json:"event"`
	TaskID string            `json:"task_id"`
	Extra  map[string]string `json:"extra"`
}

// NotifyActivity sends a themed notification via the configured ChatSender.
// If ChatSender is nil (NullSender), the message is silently dropped.
func (a *Activities) NotifyActivity(ctx context.Context, req NotifyRequest) error {
	logger := activity.GetLogger(ctx)

	msg := themed(req.Event, req.TaskID, req.Extra)
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

// themed produces a notification message for the given event.
// Returns empty string for unknown events (caller should skip sending).
func themed(event, taskID string, extra map[string]string) string {
	get := func(key string) string {
		if extra == nil {
			return ""
		}
		return extra[key]
	}

	switch event {
	case "dispatch":
		count := get("count")
		tasks := get("tasks")
		if count == "" {
			count = "?"
		}
		return fmt.Sprintf("📤 **dispatched %s tasks** — %s", count, tasks)

	case "execute":
		agent := get("agent")
		return fmt.Sprintf("⚡ **agent started** — `%s` agent=%s", taskID, agent)

	case "dod_pass":
		return fmt.Sprintf("✅ **DoD passed** — `%s`", taskID)

	case "dod_fail":
		failures := get("failures")
		if len(failures) > 200 {
			failures = failures[:200] + "…"
		}
		return fmt.Sprintf("❌ **DoD failed** — `%s`: %s", taskID, failures)

	case "complete":
		pr := get("pr")
		review := get("review_url")
		suffix := ""
		if pr != "" {
			suffix = fmt.Sprintf(" PR #%s", pr)
		}
		if review != "" {
			suffix += fmt.Sprintf(" — %s", review)
		}
		return fmt.Sprintf("🎉 **task complete** — `%s`%s", taskID, suffix)

	case "review":
		reviewer := get("reviewer")
		round := get("round")
		return fmt.Sprintf("🔍 **review started** — `%s` reviewer=%s round=%s", taskID, reviewer, round)

	case "review_approved":
		reviewer := get("reviewer")
		return fmt.Sprintf("✅ **review approved** — `%s` reviewer=%s", taskID, reviewer)

	case "review_changes":
		reviewer := get("reviewer")
		round := get("round")
		return fmt.Sprintf("🔄 **changes requested** — `%s` reviewer=%s round=%s", taskID, reviewer, round)

	case "pr_created":
		pr := get("pr")
		url := get("url")
		return fmt.Sprintf("📋 **PR opened** — `%s` #%s %s", taskID, pr, url)

	case "merged":
		pr := get("pr")
		return fmt.Sprintf("🏁 **merged to main** — `%s` PR #%s", taskID, pr)

	case "escalate":
		reason := get("reason")
		sub := get("sub_reason")
		return fmt.Sprintf("🚨 **task blocked** — `%s` reason=%s (%s)", taskID, reason, sub)

	case "decomposed":
		subtasks := get("subtasks")
		return fmt.Sprintf("🔀 **task decomposed** — `%s` into %s subtasks", taskID, subtasks)

	default:
		return ""
	}
}

// joinTasks formats task IDs as backtick-delimited list.
func joinTasks(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "`" + id + "`"
	}
	return strings.Join(parts, ", ")
}
