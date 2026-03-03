package auth

import (
	"bytes"
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

	auth := New(db)
	if err := auth.EnsureSchema(); err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	return auth
}

func TestRegisterHandler_ValidInput_Returns201(t *testing.T) {
	auth := setupTestAuth(t)

	req := RegisterRequest{
		Username: "testuser",
		Password: "password123",
	}
	body, _ := json.Marshal(req)

	r := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.RegisterHandler(w, r)

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

	// Verify user was created
	user, err := auth.GetUser("testuser")
	if err != nil {
		t.Fatalf("Failed to get created user: %v", err)
	}
	if user.Username != "testuser" {
		t.Errorf("Expected username 'testuser', got '%s'", user.Username)
	}
}

func TestRegisterHandler_ShortPassword_Returns400(t *testing.T) {
	auth := setupTestAuth(t)

	req := RegisterRequest{
		Username: "testuser",
		Password: "short", // Only 5 characters
	}
	body, _ := json.Marshal(req)

	r := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.RegisterHandler(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !strings.Contains(resp.Error, "at least 8 characters") {
		t.Errorf("Expected password length error, got '%s'", resp.Error)
	}
}

func TestRegisterHandler_EmptyUsername_Returns400(t *testing.T) {
	auth := setupTestAuth(t)

	req := RegisterRequest{
		Username: "",
		Password: "password123",
	}
	body, _ := json.Marshal(req)

	r := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.RegisterHandler(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !strings.Contains(resp.Error, "Username cannot be empty") {
		t.Errorf("Expected username empty error, got '%s'", resp.Error)
	}
}

func TestRegisterHandler_DuplicateUsername_Returns409(t *testing.T) {
	auth := setupTestAuth(t)

	// Create initial user
	if err := auth.CreateUser("testuser", "password123"); err != nil {
		t.Fatalf("Failed to create initial user: %v", err)
	}

	// Try to create duplicate
	req := RegisterRequest{
		Username: "testuser",
		Password: "password456",
	}
	body, _ := json.Marshal(req)

	r := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.RegisterHandler(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("Expected status 409, got %d", w.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !strings.Contains(resp.Error, "already exists") {
		t.Errorf("Expected user exists error, got '%s'", resp.Error)
	}
}

func TestLoginHandler_ValidCredentials_Returns200WithToken(t *testing.T) {
	auth := setupTestAuth(t)

	// Create user
	if err := auth.CreateUser("testuser", "password123"); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	req := LoginRequest{
		Username: "testuser",
		Password: "password123",
	}
	body, _ := json.Marshal(req)

	r := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.LoginHandler(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp LoginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Token == "" {
		t.Error("Expected token to be set")
	}

	if resp.ExpiresAt.Before(time.Now()) {
		t.Error("Expected expires_at to be in the future")
	}

	// Check cookie was set
	cookies := w.Result().Cookies()
	var authCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "auth_token" {
			authCookie = cookie
			break
		}
	}

	if authCookie == nil {
		t.Error("Expected auth_token cookie to be set")
	} else if authCookie.Value != resp.Token {
		t.Errorf("Expected cookie value to match token: %s != %s", authCookie.Value, resp.Token)
	}

	// Verify session was created
	session, err := auth.GetSession(resp.Token)
	if err != nil {
		t.Fatalf("Failed to get created session: %v", err)
	}
	if session.Username != "testuser" {
		t.Errorf("Expected session username 'testuser', got '%s'", session.Username)
	}
}

func TestLoginHandler_WrongPassword_Returns401(t *testing.T) {
	auth := setupTestAuth(t)

	// Create user
	if err := auth.CreateUser("testuser", "password123"); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	req := LoginRequest{
		Username: "testuser",
		Password: "wrongpassword",
	}
	body, _ := json.Marshal(req)

	r := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.LoginHandler(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !strings.Contains(resp.Error, "Invalid username or password") {
		t.Errorf("Expected invalid credentials error, got '%s'", resp.Error)
	}
}

func TestLoginHandler_NonexistentUser_Returns401(t *testing.T) {
	auth := setupTestAuth(t)

	req := LoginRequest{
		Username: "nonexistent",
		Password: "password123",
	}
	body, _ := json.Marshal(req)

	r := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	auth.LoginHandler(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !strings.Contains(resp.Error, "Invalid username or password") {
		t.Errorf("Expected invalid credentials error, got '%s'", resp.Error)
	}
}

func TestLogoutHandler_ValidToken_InvalidatesSession(t *testing.T) {
	auth := setupTestAuth(t)

	// Create user and session
	if err := auth.CreateUser("testuser", "password123"); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	token, err := auth.CreateSession("testuser", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify session exists before logout
	if _, err := auth.GetSession(token); err != nil {
		t.Fatalf("Session should exist before logout: %v", err)
	}

	// Logout with cookie
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "auth_token", Value: token})
	w := httptest.NewRecorder()

	auth.LogoutHandler(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}

	// Verify session was deleted
	if _, err := auth.GetSession(token); err != ErrSessionNotFound {
		t.Errorf("Expected session to be deleted, got error: %v", err)
	}

	// Check cookie was cleared
	cookies := w.Result().Cookies()
	var authCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "auth_token" {
			authCookie = cookie
			break
		}
	}

	if authCookie == nil {
		t.Error("Expected auth_token cookie to be cleared")
	} else if authCookie.Value != "" {
		t.Errorf("Expected cookie to be empty, got '%s'", authCookie.Value)
	}
}

func TestLogoutHandler_AuthorizationHeader_InvalidatesSession(t *testing.T) {
	auth := setupTestAuth(t)

	// Create user and session
	if err := auth.CreateUser("testuser", "password123"); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	token, err := auth.CreateSession("testuser", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Logout with Authorization header
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	auth.LogoutHandler(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}

	// Verify session was deleted
	if _, err := auth.GetSession(token); err != ErrSessionNotFound {
		t.Errorf("Expected session to be deleted, got error: %v", err)
	}
}

func TestLogoutHandler_InvalidToken_Returns204(t *testing.T) {
	auth := setupTestAuth(t)

	// Logout with invalid token (should be idempotent)
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	r.Header.Set("Authorization", "Bearer invalidtoken")
	w := httptest.NewRecorder()

	auth.LogoutHandler(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}
}

func TestLogoutHandler_NoToken_Returns204(t *testing.T) {
	auth := setupTestAuth(t)

	// Logout with no token (should be idempotent)
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	w := httptest.NewRecorder()

	auth.LogoutHandler(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}
}

func TestHandlers_InvalidMethod_ReturnsMethodNotAllowed(t *testing.T) {
	auth := setupTestAuth(t)

	testCases := []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{"RegisterHandler", auth.RegisterHandler, "/auth/register"},
		{"LoginHandler", auth.LoginHandler, "/auth/login"},
		{"LogoutHandler", auth.LogoutHandler, "/auth/logout"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()

			tc.handler(w, r)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("Expected status 405, got %d", w.Code)
			}
		})
	}
}

func TestHandlers_InvalidJSON_Returns400(t *testing.T) {
	auth := setupTestAuth(t)

	testCases := []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{"RegisterHandler", auth.RegisterHandler, "/auth/register"},
		{"LoginHandler", auth.LoginHandler, "/auth/login"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("invalid json"))
			w := httptest.NewRecorder()

			tc.handler(w, r)

			if w.Code != http.StatusBadRequest {
				t.Errorf("Expected status 400, got %d", w.Code)
			}

			var resp ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			if !strings.Contains(resp.Error, "Invalid JSON") {
				t.Errorf("Expected invalid JSON error, got '%s'", resp.Error)
			}
		})
	}
}