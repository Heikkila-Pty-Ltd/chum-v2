package auth

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *Auth {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	auth := NewAuth(db)

	ctx := context.Background()
	if err := auth.EnsureSchema(ctx); err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	return auth
}

func TestCreateUser_StoresBcryptHash(t *testing.T) {
	auth := setupTestDB(t)
	ctx := context.Background()

	// Create a user
	err := auth.CreateUser(ctx, "user1", "testuser", "plaintext_password")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	// Lookup the user and verify password is hashed
	user, err := auth.LookupUser(ctx, "testuser")
	if err != nil {
		t.Fatalf("Failed to lookup user: %v", err)
	}
	if user == nil {
		t.Fatal("User not found")
	}

	// Verify it's not plaintext
	if user.PasswordHash == "plaintext_password" {
		t.Error("Password was stored as plaintext, not hashed")
	}

	// Verify it's a valid bcrypt hash
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("plaintext_password"))
	if err != nil {
		t.Errorf("Password hash verification failed: %v", err)
	}
}

func TestCreateUser_DuplicateUsername_ReturnsError(t *testing.T) {
	auth := setupTestDB(t)
	ctx := context.Background()

	// Create first user
	err := auth.CreateUser(ctx, "user1", "testuser", "password1")
	if err != nil {
		t.Fatalf("Failed to create first user: %v", err)
	}

	// Try to create second user with same username
	err = auth.CreateUser(ctx, "user2", "testuser", "password2")
	if err == nil {
		t.Error("Expected error when creating user with duplicate username")
	}

	// Verify error is related to uniqueness constraint
	if !strings.Contains(err.Error(), "UNIQUE") && !strings.Contains(err.Error(), "constraint") {
		t.Errorf("Expected uniqueness constraint error, got: %v", err)
	}
}

func TestCreateSession_Returns64CharHexToken(t *testing.T) {
	auth := setupTestDB(t)
	ctx := context.Background()

	// Create a user first
	err := auth.CreateUser(ctx, "user1", "testuser", "password")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	// Create a session
	expiresAt := time.Now().Add(time.Hour)
	token, err := auth.CreateSession(ctx, "user1", expiresAt)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify token is 64 characters (32 bytes hex encoded)
	if len(token) != 64 {
		t.Errorf("Expected token length 64, got %d", len(token))
	}

	// Verify it's valid hex
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			t.Errorf("Token contains non-hex character: %c", c)
			break
		}
	}
}

func TestValidateSession_ValidToken_ReturnsUserID(t *testing.T) {
	auth := setupTestDB(t)
	ctx := context.Background()

	// Create a user and session
	err := auth.CreateUser(ctx, "user1", "testuser", "password")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	token, err := auth.CreateSession(ctx, "user1", expiresAt)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Validate the session
	userID, err := auth.ValidateSession(ctx, token)
	if err != nil {
		t.Fatalf("Failed to validate session: %v", err)
	}

	if userID != "user1" {
		t.Errorf("Expected user ID 'user1', got '%s'", userID)
	}
}

func TestValidateSession_ExpiredToken_ReturnsError(t *testing.T) {
	auth := setupTestDB(t)
	ctx := context.Background()

	// Create a user
	err := auth.CreateUser(ctx, "user1", "testuser", "password")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	// Create a session that expires in the past
	expiresAt := time.Now().Add(-time.Hour)
	token, err := auth.CreateSession(ctx, "user1", expiresAt)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Try to validate the expired session
	_, err = auth.ValidateSession(ctx, token)
	if err == nil {
		t.Error("Expected error for expired session")
	}

	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("Expected 'expired' in error message, got: %v", err)
	}
}

func TestValidateSession_InvalidToken_ReturnsError(t *testing.T) {
	auth := setupTestDB(t)
	ctx := context.Background()

	// Try to validate a non-existent token
	_, err := auth.ValidateSession(ctx, "nonexistent_token")
	if err == nil {
		t.Error("Expected error for invalid token")
	}

	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("Expected 'invalid' in error message, got: %v", err)
	}
}

func TestDeleteSession_InvalidatesToken(t *testing.T) {
	auth := setupTestDB(t)
	ctx := context.Background()

	// Create a user and session
	err := auth.CreateUser(ctx, "user1", "testuser", "password")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	token, err := auth.CreateSession(ctx, "user1", expiresAt)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify session is valid before deletion
	userID, err := auth.ValidateSession(ctx, token)
	if err != nil {
		t.Fatalf("Session should be valid before deletion: %v", err)
	}
	if userID != "user1" {
		t.Errorf("Expected user ID 'user1', got '%s'", userID)
	}

	// Delete the session
	err = auth.DeleteSession(ctx, token)
	if err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	// Verify session is now invalid
	_, err = auth.ValidateSession(ctx, token)
	if err == nil {
		t.Error("Session should be invalid after deletion")
	}
}

func TestLookupUser_NonExistentUser_ReturnsNil(t *testing.T) {
	auth := setupTestDB(t)
	ctx := context.Background()

	user, err := auth.LookupUser(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if user != nil {
		t.Error("Expected nil user for non-existent username")
	}
}

func TestEnsureSchema_CreatesTablesSuccessfully(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	auth := NewAuth(db)
	ctx := context.Background()

	err = auth.EnsureSchema(ctx)
	if err != nil {
		t.Fatalf("EnsureSchema failed: %v", err)
	}

	// Verify tables were created by attempting to insert data
	err = auth.CreateUser(ctx, "test1", "testuser", "password")
	if err != nil {
		t.Errorf("Failed to create user after schema creation: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	_, err = auth.CreateSession(ctx, "test1", expiresAt)
	if err != nil {
		t.Errorf("Failed to create session after schema creation: %v", err)
	}
}
