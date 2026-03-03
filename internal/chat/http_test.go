package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReadMessages_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok123" {
			t.Errorf("bad auth header: %s", got)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"chunk": []map[string]interface{}{
				{
					"event_id":         "$ev1",
					"sender":           "@alice:example.com",
					"type":             "m.room.message",
					"origin_server_ts": 1700000000000,
					"content":          map[string]string{"msgtype": "m.text", "body": "hello"},
				},
				{
					"event_id":         "$ev2",
					"sender":           "@bob:example.com",
					"type":             "m.room.message",
					"origin_server_ts": 1700000001000,
					"content":          map[string]string{"msgtype": "m.image", "body": "pic.png"},
				},
			},
			"end": "t47_end",
		})
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok123")
	msgs, end, err := client.ReadMessages(context.Background(), "!room:example.com", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if end != "t47_end" {
		t.Errorf("expected end=t47_end, got %s", end)
	}
	// Only m.text messages should be returned, not m.image.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != "$ev1" {
		t.Errorf("expected event ID $ev1, got %s", msgs[0].ID)
	}
	if msgs[0].Body != "hello" {
		t.Errorf("expected body 'hello', got %s", msgs[0].Body)
	}
	if msgs[0].Sender != "@alice:example.com" {
		t.Errorf("expected sender @alice:example.com, got %s", msgs[0].Sender)
	}
}

func TestReadMessages_WithSinceToken(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		json.NewEncoder(w).Encode(map[string]interface{}{"chunk": []interface{}{}, "end": ""})
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	_, _, err := client.ReadMessages(context.Background(), "!room:ex", "token_abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath == "" {
		t.Fatal("no request made")
	}
	if !strings.Contains(gotPath, "from=token_abc") {
		t.Errorf("expected path to contain from=token_abc, got %s", gotPath)
	}
}

func TestReadMessages_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("access denied"))
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	_, _, err := client.ReadMessages(context.Background(), "!room:ex", "")
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected error to mention 403, got: %v", err)
	}
}

func TestReadMessages_InvalidJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	_, _, err := client.ReadMessages(context.Background(), "!room:ex", "")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse messages") {
		t.Errorf("expected 'parse messages' in error, got: %v", err)
	}
}

func TestReadMessages_EmptyChunk(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"chunk": []interface{}{}, "end": "tok"})
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	msgs, _, err := client.ReadMessages(context.Background(), "!room:ex", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestWhoAmI_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/account/whoami") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{"user_id": "@bot:example.com"})
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	userID, err := client.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "@bot:example.com" {
		t.Errorf("expected @bot:example.com, got %s", userID)
	}
}

func TestWhoAmI_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("bad token"))
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	_, err := client.WhoAmI(context.Background())
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected error to mention 401, got: %v", err)
	}
}

func TestWhoAmI_InvalidJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("{broken"))
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	_, err := client.WhoAmI(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSendMessage_Success(t *testing.T) {
	t.Parallel()
	var gotMethod string
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"event_id": "$sent1"})
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok123")
	txnID, err := client.SendMessage(context.Background(), "!room:ex", "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txnID == "" {
		t.Error("expected non-empty txnID")
	}
	if gotMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected application/json, got %s", gotContentType)
	}
	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("failed to parse sent body: %v", err)
	}
	if payload["msgtype"] != "m.text" {
		t.Errorf("expected msgtype m.text, got %s", payload["msgtype"])
	}
	if payload["body"] != "hello world" {
		t.Errorf("expected body 'hello world', got %s", payload["body"])
	}
}

func TestSendMessage_EmptyMessage(t *testing.T) {
	t.Parallel()
	client := NewMatrixClient("http://unused", "tok")
	txnID, err := client.SendMessage(context.Background(), "!room:ex", "   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txnID != "" {
		t.Error("expected empty txnID for blank message")
	}
}

func TestSendMessage_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	_, err := client.SendMessage(context.Background(), "!room:ex", "fail")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention 500, got: %v", err)
	}
}

func TestSendMessage_IncrementsTxnCounter(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	_, _ = client.SendMessage(context.Background(), "!room:ex", "first")
	_, _ = client.SendMessage(context.Background(), "!room:ex", "second")
	if got := atomic.LoadUint64(&client.txnCounter); got != 2 {
		t.Errorf("expected txnCounter=2, got %d", got)
	}
}

func TestNewMatrixClient_TrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	client := NewMatrixClient("https://matrix.example.com/", "tok")
	if client.homeserver != "https://matrix.example.com" {
		t.Errorf("expected trailing slash trimmed, got %s", client.homeserver)
	}
}

func TestDoGet_ContextCanceled(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewMatrixClient(srv.URL, "tok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.WhoAmI(ctx)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}
