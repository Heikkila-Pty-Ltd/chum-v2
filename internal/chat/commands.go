package chat

import (
	"fmt"
	"strings"
)

// CommandKind identifies the type of planning command.
type CommandKind int

const (
	CommandHelp CommandKind = iota + 1
	CommandStart
	CommandPrompt
	CommandStatus
	CommandSelect
	CommandAnswer
	CommandDig
	CommandGo
	CommandApprove
	CommandRealign
	CommandStop
)

// Command is a parsed planning command from chat.
type Command struct {
	Kind      CommandKind
	Project   string
	Agent     string
	SessionID string
	Value     string // goal ID, approach ID, answer text, or dig target
	Reason    string
}

// ParseCommand parses a /plan command from a chat message body.
// Returns the parsed command, whether the message matched the /plan prefix,
// and any parse error.
func ParseCommand(raw string) (Command, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return Command{}, false, nil
	}
	lower := strings.ToLower(text)
	const prefix = "/plan"
	if !strings.HasPrefix(lower, prefix) {
		return Command{}, false, nil
	}
	if len(lower) > len(prefix) {
		next := lower[len(prefix)]
		if next != ' ' && next != '\t' && next != '\n' && next != '\r' {
			return Command{}, false, nil
		}
	}
	text = strings.TrimSpace(text[len(prefix):])

	if text == "" {
		return Command{Kind: CommandHelp}, true, nil
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return Command{Kind: CommandHelp}, true, nil
	}

	action := strings.ToLower(strings.TrimSpace(parts[0]))
	args := parts[1:]

	switch action {
	case "help":
		return Command{Kind: CommandHelp}, true, nil

	case "start":
		cmd := Command{Kind: CommandStart}
		if len(args) == 0 {
			return Command{}, true, fmt.Errorf("missing project")
		}
		cursor := 0
		// First positional arg is the project name (or goal ID after project=).
		if !strings.Contains(args[cursor], "=") {
			cmd.Project = args[cursor]
			cursor++
		}
		// Remaining positional arg (if not key=value) is the goal ID.
		if cursor < len(args) && !strings.Contains(args[cursor], "=") {
			cmd.Value = args[cursor] // goal ID
			cursor++
		}
		for ; cursor < len(args); cursor++ {
			k, v, ok := strings.Cut(args[cursor], "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "project":
				cmd.Project = strings.TrimSpace(v)
			case "goal", "goal_id":
				cmd.Value = strings.TrimSpace(v)
			case "agent":
				cmd.Agent = strings.TrimSpace(v)
			}
		}
		if strings.TrimSpace(cmd.Project) == "" {
			return Command{}, true, fmt.Errorf("missing project")
		}
		if strings.TrimSpace(cmd.Value) == "" {
			return Command{}, true, fmt.Errorf("missing goal id")
		}
		return cmd, true, nil

	case "prompt":
		cmd := Command{Kind: CommandPrompt}
		if len(args) > 0 {
			sessionID, ok := parseSessionToken(args[0])
			if !ok {
				return Command{}, true, fmt.Errorf("invalid session token %q", args[0])
			}
			cmd.SessionID = sessionID
		}
		return cmd, true, nil

	case "status":
		cmd := Command{Kind: CommandStatus}
		if len(args) > 0 {
			sessionID, ok := parseSessionToken(args[0])
			if !ok {
				return Command{}, true, fmt.Errorf("invalid session token %q", args[0])
			}
			cmd.SessionID = sessionID
		}
		return cmd, true, nil

	case "select":
		if len(args) == 0 {
			return Command{}, true, fmt.Errorf("missing item id")
		}
		cmd := Command{Kind: CommandSelect}
		if sid, ok := parseSessionToken(args[0]); ok && len(args) > 1 {
			cmd.SessionID = sid
			cmd.Value = strings.TrimSpace(args[1])
			return cmd, true, nil
		}
		cmd.Value = strings.TrimSpace(args[0])
		if len(args) > 1 {
			if sid, ok := parseSessionToken(args[1]); ok {
				cmd.SessionID = sid
			}
		}
		return cmd, true, nil

	case "dig":
		if len(args) == 0 {
			return Command{}, true, fmt.Errorf("missing approach id")
		}
		cmd := Command{Kind: CommandDig}
		cmd.Value = strings.TrimSpace(args[0])
		if len(args) > 1 {
			cmd.Reason = strings.TrimSpace(strings.Join(args[1:], " "))
		}
		return cmd, true, nil

	case "answer":
		if len(args) == 0 {
			return Command{}, true, fmt.Errorf("missing answer text")
		}
		cmd := Command{Kind: CommandAnswer}
		startIdx := 0
		if sid, ok := parseSessionToken(args[0]); ok {
			cmd.SessionID = sid
			startIdx = 1
		}
		cmd.Value = strings.TrimSpace(strings.Join(args[startIdx:], " "))
		if cmd.Value == "" {
			return Command{}, true, fmt.Errorf("missing answer text")
		}
		return cmd, true, nil

	case "go":
		cmd := Command{Kind: CommandGo}
		if len(args) > 0 {
			sessionID, ok := parseSessionToken(args[0])
			if !ok {
				return Command{}, true, fmt.Errorf("invalid session token %q", args[0])
			}
			cmd.SessionID = sessionID
		}
		return cmd, true, nil

	case "approve":
		cmd := Command{Kind: CommandApprove}
		if len(args) > 0 {
			sessionID, ok := parseSessionToken(args[0])
			if !ok {
				return Command{}, true, fmt.Errorf("invalid session token %q", args[0])
			}
			cmd.SessionID = sessionID
		}
		return cmd, true, nil

	case "realign", "reject", "no":
		cmd := Command{Kind: CommandRealign}
		if len(args) > 0 {
			sessionID, ok := parseSessionToken(args[0])
			if !ok {
				return Command{}, true, fmt.Errorf("invalid session token %q", args[0])
			}
			cmd.SessionID = sessionID
		}
		return cmd, true, nil

	case "stop", "cancel":
		cmd := Command{Kind: CommandStop}
		startIdx := 0
		if len(args) > 0 {
			if sid, ok := parseSessionToken(args[0]); ok {
				cmd.SessionID = sid
				startIdx = 1
			}
		}
		if startIdx < len(args) {
			cmd.Reason = strings.TrimSpace(strings.Join(args[startIdx:], " "))
		}
		return cmd, true, nil

	default:
		return Command{}, true, fmt.Errorf("unknown action %q", action)
	}
}

// CommandUsage returns the help text for planning commands.
func CommandUsage() string {
	return `Planning control commands:
- /plan start <project> <goal-id> [agent=claude]
- /plan prompt [session]
- /plan status [session]
- /plan select <approach-id> [session]
- /plan dig <approach-id> [feedback...]
- /plan answer [session] <text>
- /plan go [session]
- /plan approve [session]
- /plan realign [session]
- /plan stop [session] [reason]

If session is omitted, the room's active planning session is used.`
}

func parseSessionToken(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(raw), "session=") {
		value := strings.TrimSpace(raw[len("session="):])
		return value, value != ""
	}
	if strings.HasPrefix(raw, "planning-") {
		return raw, true
	}
	return "", false
}
