package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestAuth(t *testing.T) *Auth {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	auth := NewAuth(db)
	if err := auth.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("Failed to ensure schema: %v", err)
	}

	return auth
}

func TestRegisterHandler_ValidInput(t *testing.T) {
	auth := setupTestAuth(t)

	payload := RegisterRequest{
		Username: "testuser",
		Password: "password123",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.RegisterHandler(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", w.Code)
	}

	var resp RegisterResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Username != "testuser" {
		t.Errorf("Expected username 'testuser', got '%s'", resp.Username)
	}
}

func TestRegisterHandler_ShortPassword(t *testing.T) {
	auth := setupTestAuth(t)

	payload := RegisterRequest{
		Username: "testuser",
		Password: "short",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.RegisterHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "at least 8 characters") {
		t.Errorf("Expected password length error message")
	}
}

func TestRegisterHandler_DuplicateUsername(t *testing.T) {
	auth := setupTestAuth(t)

	// Create first user
	payload1 := RegisterRequest{
		Username: "testuser",
		Password: "password123",
	}
	body1, _ := json.Marshal(payload1)

	req1 := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body1))
	w1 := httptest.NewRecorder()
	auth.RegisterHandler(w1, req1)

	if w1.Code != http.StatusCreated {
		t.Fatalf("First registration should succeed, got %d", w1.Code)
	}

	// Try to create duplicate user
	payload2 := RegisterRequest{
		Username: "testuser",
		Password: "password456",
	}
	body2, _ := json.Marshal(payload2)

	req2 := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body2))
	w2 := httptest.NewRecorder()
	auth.RegisterHandler(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("Expected status 409, got %d", w2.Code)
	}

	if !strings.Contains(w2.Body.String(), "already exists") {
		t.Errorf("Expected username exists error message")
	}
}

func TestLoginHandler_ValidCredentials(t *testing.T) {
	auth := setupTestAuth(t)

	// Register user first
	userID := "test-user-id"
	username := "testuser"
	password := "password123"
	err := auth.CreateUser(context.Background(), userID, username, password)
	if err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}

	// Login
	payload := LoginRequest{
		Username: username,
		Password: password,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.LoginHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp LoginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Token == "" {
		t.Errorf("Expected non-empty token")
	}

	if resp.ExpiresAt.IsZero() {
		t.Errorf("Expected non-zero expires_at")
	}

	// Check cookie is set
	cookies := w.Result().Cookies()
	foundCookie := false
	for _, cookie := range cookies {
		if cookie.Name == "session_token" && cookie.Value == resp.Token {
			foundCookie = true
			break
		}
	}
	if !foundCookie {
		t.Errorf("Expected session_token cookie to be set")
	}
}

func TestLoginHandler_WrongPassword(t *testing.T) {
	auth := setupTestAuth(t)

	// Register user first
	userID := "test-user-id"
	username := "testuser"
	password := "password123"
	err := auth.CreateUser(context.Background(), userID, username, password)
	if err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}

	// Login with wrong password
	payload := LoginRequest{
		Username: username,
		Password: "wrongpassword",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.LoginHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "Invalid credentials") {
		t.Errorf("Expected invalid credentials error message")
	}
}

func TestLoginHandler_NonexistentUser(t *testing.T) {
	auth := setupTestAuth(t)

	// Login with non-existent user
	payload := LoginRequest{
		Username: "nonexistent",
		Password: "password123",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.LoginHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "Invalid credentials") {
		t.Errorf("Expected invalid credentials error message")
	}
}

func TestLogoutHandler_InvalidatesSession(t *testing.T) {
	auth := setupTestAuth(t)

	// Create user and session
	userID := "test-user-id"
	username := "testuser"
	password := "password123"
	err := auth.CreateUser(context.Background(), userID, username, password)
	if err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	token, err := auth.CreateSession(context.Background(), userID, expiresAt)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify session is valid before logout
	_, err = auth.ValidateSession(context.Background(), token)
	if err != nil {
		t.Fatalf("Session should be valid before logout: %v", err)
	}

	// Logout with cookie
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	w := httptest.NewRecorder()

	auth.LogoutHandler(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}

	// Verify session is invalid after logout
	_, err = auth.ValidateSession(context.Background(), token)
	if err == nil {
		t.Errorf("Session should be invalid after logout")
	}
}

func TestLogoutHandler_AuthorizationHeader(t *testing.T) {
	auth := setupTestAuth(t)

	// Create user and session
	userID := "test-user-id"
	username := "testuser"
	password := "password123"
	err := auth.CreateUser(context.Background(), userID, username, password)
	if err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	token, err := auth.CreateSession(context.Background(), userID, expiresAt)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Logout with Authorization header
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	auth.LogoutHandler(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}

	// Verify session is invalid after logout
	_, err = auth.ValidateSession(context.Background(), token)
	if err == nil {
		t.Errorf("Session should be invalid after logout")
	}
}

func TestLogoutHandler_InvalidToken_Idempotent(t *testing.T) {
	auth := setupTestAuth(t)

	// Logout with invalid/missing token
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	w := httptest.NewRecorder()

	auth.LogoutHandler(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}

	// Logout with invalid token in cookie
	req2 := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req2.AddCookie(&http.Cookie{Name: "session_token", Value: "invalid-token"})
	w2 := httptest.NewRecorder()

	auth.LogoutHandler(w2, req2)

	if w2.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w2.Code)
	}

	// Logout with invalid token in Authorization header
	req3 := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req3.Header.Set("Authorization", "Bearer invalid-token")
	w3 := httptest.NewRecorder()

	auth.LogoutHandler(w3, req3)

	if w3.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w3.Code)
	}
}