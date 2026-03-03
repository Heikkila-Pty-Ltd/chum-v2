package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
)

func TestHealthEndpoint_TemporalUnreachable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := &config.Config{
		General: config.General{
			TemporalHostPort:  "localhost:9999", // Non-existent port
			TemporalNamespace: "test-namespace",
		},
	}

	server := NewServer(cfg, logger)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}

	var response HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "unhealthy" {
		t.Errorf("Expected status 'unhealthy', got '%s'", response.Status)
	}

	if response.Error == "" {
		t.Error("Expected error message to be present")
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Expected Content-Type header to be 'application/json'")
	}
}

func TestHealthEndpoint_MethodNotAllowed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := &config.Config{
		General: config.General{
			TemporalHostPort:  "localhost:7233",
			TemporalNamespace: "test-namespace",
		},
	}

	server := NewServer(cfg, logger)

	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestHealthEndpoint_ResponseTime(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := &config.Config{
		General: config.General{
			TemporalHostPort:  "localhost:9999", // Non-existent port to ensure timeout
			TemporalNamespace: "test-namespace",
		},
	}

	server := NewServer(cfg, logger)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	server.handleHealth(w, req)
	duration := time.Since(start)

	// Should respond within 3 seconds (2s timeout + overhead)
	if duration > 3*time.Second {
		t.Errorf("Response took too long: %v", duration)
	}
}

func TestStartServer_ContextCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := &config.Config{
		General: config.General{
			TemporalHostPort:  "localhost:7233",
			TemporalNamespace: "test-namespace",
		},
	}

	server := NewServer(cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx, ":0")
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Wait for server to stop
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Server returned error during shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Server did not shut down within expected time")
	}
}