package chat

import "testing"

func TestParseCommand_EmptyString(t *testing.T) {
	t.Parallel()
	_, matched, _ := ParseCommand("")
	if matched {
		t.Fatal("expected no match for empty string")
	}
}

func TestParseCommand_WhitespaceOnly(t *testing.T) {
	t.Parallel()
	_, matched, _ := ParseCommand("   ")
	if matched {
		t.Fatal("expected no match for whitespace")
	}
}

func TestParseCommand_CaseInsensitive(t *testing.T) {
	t.Parallel()
	cases := []string{"/PLAN help", "/Plan Help", "/pLaN HELP"}
	for _, tc := range cases {
		cmd, matched, err := ParseCommand(tc)
		if !matched {
			t.Fatalf("expected match for %q", tc)
		}
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", tc, err)
		}
		if cmd.Kind != CommandHelp {
			t.Fatalf("expected CommandHelp for %q, got %d", tc, cmd.Kind)
		}
	}
}

func TestParseCommand_HelpExplicit(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan help")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandHelp {
		t.Fatalf("expected CommandHelp, got %d", cmd.Kind)
	}
}

func TestParseCommand_SelectMissingArg(t *testing.T) {
	t.Parallel()
	_, matched, err := ParseCommand("/plan select")
	if !matched {
		t.Fatal("expected match")
	}
	if err == nil {
		t.Fatal("expected error for missing item id")
	}
}

func TestParseCommand_DigMissingArg(t *testing.T) {
	t.Parallel()
	_, matched, err := ParseCommand("/plan dig")
	if !matched {
		t.Fatal("expected match")
	}
	if err == nil {
		t.Fatal("expected error for missing approach id")
	}
}

func TestParseCommand_AnswerMissingArg(t *testing.T) {
	t.Parallel()
	_, matched, err := ParseCommand("/plan answer")
	if !matched {
		t.Fatal("expected match")
	}
	if err == nil {
		t.Fatal("expected error for missing answer text")
	}
}

func TestParseCommand_AnswerWithSession(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan answer planning-abc123 yes use Redis")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandAnswer {
		t.Fatalf("expected CommandAnswer, got %d", cmd.Kind)
	}
	if cmd.SessionID != "planning-abc123" {
		t.Fatalf("expected session=planning-abc123, got %q", cmd.SessionID)
	}
	if cmd.Value != "yes use Redis" {
		t.Fatalf("expected answer text, got %q", cmd.Value)
	}
}

func TestParseCommand_AnswerSessionOnly(t *testing.T) {
	t.Parallel()
	// Session token but no answer text after it
	_, matched, err := ParseCommand("/plan answer planning-abc123")
	if !matched {
		t.Fatal("expected match")
	}
	if err == nil {
		t.Fatal("expected error for missing answer text after session")
	}
}

func TestParseCommand_StopWithSession(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan stop planning-abc123 no longer needed")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandStop {
		t.Fatalf("expected CommandStop, got %d", cmd.Kind)
	}
	if cmd.SessionID != "planning-abc123" {
		t.Fatalf("expected session=planning-abc123, got %q", cmd.SessionID)
	}
	if cmd.Reason != "no longer needed" {
		t.Fatalf("expected reason, got %q", cmd.Reason)
	}
}

func TestParseCommand_StopNoArgs(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan stop")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandStop {
		t.Fatalf("expected CommandStop, got %d", cmd.Kind)
	}
	if cmd.Reason != "" {
		t.Fatalf("expected empty reason, got %q", cmd.Reason)
	}
}

func TestParseCommand_CancelAlias(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan cancel")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandStop {
		t.Fatalf("expected CommandStop for cancel alias, got %d", cmd.Kind)
	}
}

func TestParseCommand_RejectAlias(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan reject")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandRealign {
		t.Fatalf("expected CommandRealign for reject alias, got %d", cmd.Kind)
	}
}

func TestParseCommand_NoAlias(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan no")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandRealign {
		t.Fatalf("expected CommandRealign for no alias, got %d", cmd.Kind)
	}
}

func TestParseCommand_GoWithSession(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan go session=my-session-123")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandGo {
		t.Fatalf("expected CommandGo, got %d", cmd.Kind)
	}
	if cmd.SessionID != "my-session-123" {
		t.Fatalf("expected session=my-session-123, got %q", cmd.SessionID)
	}
}

func TestParseCommand_ApproveWithSession(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan approve planning-xyz")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandApprove {
		t.Fatalf("expected CommandApprove, got %d", cmd.Kind)
	}
	if cmd.SessionID != "planning-xyz" {
		t.Fatalf("expected session=planning-xyz, got %q", cmd.SessionID)
	}
}

func TestParseCommand_SelectSessionAfterValue(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan select 3 planning-sess")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandSelect {
		t.Fatalf("expected CommandSelect, got %d", cmd.Kind)
	}
	if cmd.Value != "3" {
		t.Fatalf("expected value=3, got %q", cmd.Value)
	}
	if cmd.SessionID != "planning-sess" {
		t.Fatalf("expected session=planning-sess, got %q", cmd.SessionID)
	}
}

func TestParseCommand_DigNoReason(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan dig 2")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandDig {
		t.Fatalf("expected CommandDig, got %d", cmd.Kind)
	}
	if cmd.Value != "2" {
		t.Fatalf("expected value=2, got %q", cmd.Value)
	}
	if cmd.Reason != "" {
		t.Fatalf("expected empty reason, got %q", cmd.Reason)
	}
}

func TestParseCommand_PromptNoSession(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan prompt")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandPrompt {
		t.Fatalf("expected CommandPrompt, got %d", cmd.Kind)
	}
	if cmd.SessionID != "" {
		t.Fatalf("expected empty session, got %q", cmd.SessionID)
	}
}

func TestParseCommand_StatusWithSession(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan status planning-abc")
	if !matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if cmd.Kind != CommandStatus {
		t.Fatalf("expected CommandStatus, got %d", cmd.Kind)
	}
	if cmd.SessionID != "planning-abc" {
		t.Fatalf("expected session=planning-abc, got %q", cmd.SessionID)
	}
}

func TestParseSessionToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		input     string
		wantID    string
		wantMatch bool
	}{
		{"empty", "", "", false},
		{"whitespace", "   ", "", false},
		{"planning prefix", "planning-abc123", "planning-abc123", true},
		{"session= prefix", "session=my-session", "my-session", true},
		{"session= empty value", "session=", "", false},
		{"random word", "foobar", "", false},
		{"number", "42", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, ok := parseSessionToken(tc.input)
			if ok != tc.wantMatch {
				t.Fatalf("parseSessionToken(%q) matched=%v, want %v", tc.input, ok, tc.wantMatch)
			}
			if id != tc.wantID {
				t.Fatalf("parseSessionToken(%q) id=%q, want %q", tc.input, id, tc.wantID)
			}
		})
	}
}

func TestCommandUsage_NotEmpty(t *testing.T) {
	t.Parallel()
	usage := CommandUsage()
	if usage == "" {
		t.Fatal("expected non-empty usage text")
	}
}
