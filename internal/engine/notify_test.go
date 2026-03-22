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
	sent [][]string // [roomID, message]
	err  error
}

func (m *mockSender) Send(_ context.Context, roomID, message string) error {
	m.sent = append(m.sent, []string{roomID, message})
	return m.err
}

func TestNotifyActivity_NilSender(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	a := &Activities{ChatSend: nil}
	env.RegisterActivity(a)

	_, err := env.ExecuteActivity(a.NotifyActivity, "hello")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestNotifyActivity_EmptyMessage(t *testing.T) {
	ms := &mockSender{}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	a := &Activities{ChatSend: ms}
	env.RegisterActivity(a)

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
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	a := &Activities{ChatSend: ms}
	env.RegisterActivity(a)

	_, err := env.ExecuteActivity(a.NotifyActivity, "task done")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(ms.sent) != 1 || ms.sent[0][1] != "task done" {
		t.Fatalf("expected 1 send of 'task done', got %v", ms.sent)
	}
}

func TestNotifyActivity_SendFailure(t *testing.T) {
	ms := &mockSender{err: errors.New("webhook 503")}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	a := &Activities{ChatSend: ms}
	env.RegisterActivity(a)

	_, err := env.ExecuteActivity(a.NotifyActivity, "task done")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNotifyActivity_WithRoomID(t *testing.T) {
	ms := &mockSender{}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	a := &Activities{ChatSend: ms, Config: &config.Config{General: config.General{MatrixRoomID: "!room:server"}}}
	env.RegisterActivity(a)

	_, err := env.ExecuteActivity(a.NotifyActivity, "hello")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(ms.sent) != 1 || ms.sent[0][0] != "!room:server" {
		t.Fatalf("expected room '!room:server', got %v", ms.sent)
	}
}

func TestNotifyActivity_NullSender(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	a := &Activities{ChatSend: &notify.NullSender{}}
	env.RegisterActivity(a)

	_, err := env.ExecuteActivity(a.NotifyActivity, "dropped message")
	if err != nil {
		t.Fatalf("NullSender should not error, got %v", err)
	}
}
