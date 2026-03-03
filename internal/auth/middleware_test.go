package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupMiddlewareTest(t *testing.T) (*Auth, string) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	a := NewAuth(db)
	ctx := context.Background()
	if err := a.EnsureSchema(ctx); err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	// Create a test user and session
	if err := a.CreateUser(ctx, "user1", "testuser", "password"); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	token, err := a.CreateSession(ctx, "user1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	return a, token
}

func TestRequireAuth_ValidCookie_PassesThrough(t *testing.T) {
	a, token := setupMiddlewareTest(t)

	var gotUserID string
	handler := RequireAuth(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rr.Code)
	}
	if gotUserID != "user1" {
		t.Errorf("Expected user_id 'user1' in context, got '%s'", gotUserID)
	}
}

func TestRequireAuth_ValidBearerToken_PassesThrough(t *testing.T) {
	a, token := setupMiddlewareTest(t)

	var gotUserID string
	handler := RequireAuth(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rr.Code)
	}
	if gotUserID != "user1" {
		t.Errorf("Expected user_id 'user1' in context, got '%s'", gotUserID)
	}
}

func TestRequireAuth_NoToken_Returns401(t *testing.T) {
	a, _ := setupMiddlewareTest(t)

	handler := RequireAuth(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", rr.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["error"] != "unauthorized" {
		t.Errorf("Expected error 'unauthorized', got '%s'", resp["error"])
	}
}

func TestRequireAuth_ExpiredSession_Returns401(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	a := NewAuth(db)
	ctx := context.Background()
	if err := a.EnsureSchema(ctx); err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}
	if err := a.CreateUser(ctx, "user1", "testuser", "password"); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	// Create an already-expired session
	token, err := a.CreateSession(ctx, "user1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	handler := RequireAuth(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", rr.Code)
	}
}

func TestRequireAuth_InvalidToken_Returns401(t *testing.T) {
	a, _ := setupMiddlewareTest(t)

	handler := RequireAuth(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "totally_bogus_token"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", rr.Code)
	}
}

func TestUserFromContext_NoValue_ReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	if got := UserFromContext(ctx); got != "" {
		t.Errorf("Expected empty string, got '%s'", got)
	}
}
