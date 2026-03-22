package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/notify"
	"go.temporal.io/sdk/testsuite"
)

type mockSender struct {
	sent [][]string
	err  error
}

func (m *mockSender) Send(_ context.Context, roomID, message string) error {
	m.sent = append(m.sent, []string{roomID, message})
	return m.err
}

func newEnv(a *Activities) *testsuite.TestActivityEnvironment {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.NotifyActivity)
	return env
}

func TestNotifyActivity_NilSender(t *testing.T) {
	a := &Activities{ChatSend: nil}
	env := newEnv(a)
	_, err := env.ExecuteActivity(a.NotifyActivity, "hello")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestNotifyActivity_EmptyMessage(t *testing.T) {
	ms := &mockSender{}
	a := &Activities{ChatSend: ms}
	env := newEnv(a)
	_, err := env.ExecuteActivity(a.NotifyActivity, "  \n  ")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(ms.sent) != 0 {
		t.Fatalf("expected no sends, got %d", len(ms.sent))
	}
}

func TestNotifyActivity_Sends(t *testing.T) {
	ms := &mockSender{}
	a := &Activities{ChatSend: ms}
	env := newEnv(a)
	_, err := env.ExecuteActivity(a.NotifyActivity, "task done")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(ms.sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(ms.sent))
	}
	if ms.sent[0][1] != "task done" {
		t.Fatalf("wrong message: %q", ms.sent[0][1])
	}
}

func TestNotifyActivity_SendFailure(t *testing.T) {
	ms := &mockSender{err: errors.New("webhook 503")}
	a := &Activities{ChatSend: ms}
	env := newEnv(a)
	_, err := env.ExecuteActivity(a.NotifyActivity, "task done")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNotifyActivity_WithRoomID(t *testing.T) {
	ms := &mockSender{}
	a := &Activities{ChatSend: ms, Config: &config.Config{General: config.General{MatrixRoomID: "!room:server"}}}
	env := newEnv(a)
	_, err := env.ExecuteActivity(a.NotifyActivity, "hello")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(ms.sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(ms.sent))
	}
	if ms.sent[0][0] != "!room:server" {
		t.Fatalf("wrong room: %q", ms.sent[0][0])
	}
}

func TestNotifyActivity_NullSender(t *testing.T) {
	ns := &notify.NullSender{}
	a := &Activities{ChatSend: ns}
	env := newEnv(a)
	_, err := env.ExecuteActivity(a.NotifyActivity, "dropped message")
	if err != nil {
		t.Fatalf("NullSender should not error, got %v", err)
	}
}
