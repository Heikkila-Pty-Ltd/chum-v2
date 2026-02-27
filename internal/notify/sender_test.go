package notify

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNullSenderNoOp(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ns := &NullSender{Logger: logger}
	if err := ns.Send(context.Background(), "!room:test", "hello"); err != nil {
		t.Fatalf("NullSender.Send returned error: %v", err)
	}
}

func TestNullSenderSatisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ ChatSender = (*NullSender)(nil)
}

func TestMatrixSenderSatisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ ChatSender = (*MatrixSender)(nil)
}

func TestWebhookSenderSatisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ ChatSender = (*WebhookSender)(nil)
}

func TestMatrixSenderSend(t *testing.T) {
	t.Parallel()
	var gotBody string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
		w.Write([]byte(`{"event_id":"$test"}`))
	}))
	defer srv.Close()

	ms := NewMatrixSender(srv.URL, "tok123")
	err := ms.Send(context.Background(), "!room:test", "hello world")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("unexpected auth header: %s", gotAuth)
	}
	if !strings.Contains(gotBody, `"body":"hello world"`) {
		t.Errorf("unexpected body: %s", gotBody)
	}
}

func TestMatrixSenderEmptyMessage(t *testing.T) {
	t.Parallel()
	ms := NewMatrixSender("http://unused", "tok")
	err := ms.Send(context.Background(), "!room:test", "  ")
	if err != nil {
		t.Fatalf("empty message should not error: %v", err)
	}
}

func TestWebhookSenderSend(t *testing.T) {
	t.Parallel()
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ws := NewWebhookSender(srv.URL)
	err := ws.Send(context.Background(), "", "test message")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if !strings.Contains(gotBody, `"text":"test message"`) {
		t.Errorf("unexpected body: %s", gotBody)
	}
}

func TestWebhookSenderHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	ws := NewWebhookSender(srv.URL)
	err := ws.Send(context.Background(), "", "test")
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
}
