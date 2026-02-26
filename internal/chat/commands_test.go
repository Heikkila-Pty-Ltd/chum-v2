package chat

import (
	"testing"
)

func TestParseCommand_Help(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Kind != CommandHelp {
		t.Fatalf("expected CommandHelp, got %d", cmd.Kind)
	}
}

func TestParseCommand_Start(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan start myproject /home/work agent=gemini topk=3")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Kind != CommandStart {
		t.Fatalf("expected CommandStart, got %d", cmd.Kind)
	}
	if cmd.Project != "myproject" {
		t.Fatalf("expected project=myproject, got %q", cmd.Project)
	}
	if cmd.WorkDir != "/home/work" {
		t.Fatalf("expected workdir=/home/work, got %q", cmd.WorkDir)
	}
	if cmd.Agent != "gemini" {
		t.Fatalf("expected agent=gemini, got %q", cmd.Agent)
	}
	if cmd.TopK != 3 {
		t.Fatalf("expected topk=3, got %d", cmd.TopK)
	}
}

func TestParseCommand_StartMissingProject(t *testing.T) {
	t.Parallel()
	_, matched, err := ParseCommand("/plan start")
	if !matched {
		t.Fatal("expected match")
	}
	if err == nil {
		t.Fatal("expected error for missing project")
	}
}

func TestParseCommand_Select(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan select 2")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Kind != CommandSelect {
		t.Fatalf("expected CommandSelect, got %d", cmd.Kind)
	}
	if cmd.Value != "2" {
		t.Fatalf("expected value=2, got %q", cmd.Value)
	}
}

func TestParseCommand_Dig(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan dig 1 explore the OAuth approach more")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Kind != CommandDig {
		t.Fatalf("expected CommandDig, got %d", cmd.Kind)
	}
	if cmd.Value != "1" {
		t.Fatalf("expected value=1, got %q", cmd.Value)
	}
	if cmd.Reason != "explore the OAuth approach more" {
		t.Fatalf("expected reason, got %q", cmd.Reason)
	}
}

func TestParseCommand_Answer(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan answer what about Redis caching?")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Kind != CommandAnswer {
		t.Fatalf("expected CommandAnswer, got %d", cmd.Kind)
	}
	if cmd.Value != "what about Redis caching?" {
		t.Fatalf("expected answer text, got %q", cmd.Value)
	}
}

func TestParseCommand_Go(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan go")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Kind != CommandGo {
		t.Fatalf("expected CommandGo, got %d", cmd.Kind)
	}
}

func TestParseCommand_Realign(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan realign")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Kind != CommandRealign {
		t.Fatalf("expected CommandRealign, got %d", cmd.Kind)
	}
}

func TestParseCommand_Stop(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan stop not needed anymore")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Kind != CommandStop {
		t.Fatalf("expected CommandStop, got %d", cmd.Kind)
	}
	if cmd.Reason != "not needed anymore" {
		t.Fatalf("expected reason, got %q", cmd.Reason)
	}
}

func TestParseCommand_NotMatched(t *testing.T) {
	t.Parallel()
	_, matched, _ := ParseCommand("hello world")
	if matched {
		t.Fatal("expected no match for non-plan message")
	}
}

func TestParseCommand_UnknownAction(t *testing.T) {
	t.Parallel()
	_, matched, err := ParseCommand("/plan foobar")
	if !matched {
		t.Fatal("expected match")
	}
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestParseCommand_WithSession(t *testing.T) {
	t.Parallel()
	cmd, matched, err := ParseCommand("/plan select planning-abc123 item-1")
	if !matched {
		t.Fatal("expected match")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.SessionID != "planning-abc123" {
		t.Fatalf("expected sessionID=planning-abc123, got %q", cmd.SessionID)
	}
	if cmd.Value != "item-1" {
		t.Fatalf("expected value=item-1, got %q", cmd.Value)
	}
}
