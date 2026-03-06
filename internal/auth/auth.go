package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const usersTableSchema = `CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
)`

const sessionsTableSchema = `CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
)`

// Auth wraps a SQL database for authentication operations
type Auth struct {
	db *sql.DB
}

// NewAuth creates a new Auth instance with the provided database
func NewAuth(db *sql.DB) *Auth {
	return &Auth{db: db}
}

// User represents a user in the system
type User struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// EnsureSchema creates the users and sessions tables if they don't exist
func (a *Auth) EnsureSchema(ctx context.Context) error {
	if _, err := a.db.ExecContext(ctx, usersTableSchema); err != nil {
		return fmt.Errorf("failed to create users table: %w", err)
	}
	if _, err := a.db.ExecContext(ctx, sessionsTableSchema); err != nil {
		return fmt.Errorf("failed to create sessions table: %w", err)
	}
	return nil
}

// CreateUser creates a new user with a bcrypt-hashed password
func (a *Auth) CreateUser(ctx context.Context, id, username, password string) error {
	// Hash the password using bcrypt
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Insert the user into the database
	_, err = a.db.ExecContext(ctx,
		"INSERT INTO users (id, username, password_hash) VALUES (?, ?, ?)",
		id, username, string(hashedPassword))
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// LookupUser retrieves a user by username
func (a *Auth) LookupUser(ctx context.Context, username string) (*User, error) {
	user := &User{}
	err := a.db.QueryRowContext(ctx,
		"SELECT id, username, password_hash, created_at FROM users WHERE username = ?",
		username).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to lookup user: %w", err)
	}

	return user, nil
}

// CreateSession creates a new session for the given user and returns the token
func (a *Auth) CreateSession(ctx context.Context, userID string, expiresAt time.Time) (string, error) {
	// Generate a 32-byte random token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate session token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	// Insert the session into the database
	_, err := a.db.ExecContext(ctx,
		"INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)",
		token, userID, expiresAt)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return token, nil
}

// ValidateSession validates a session token and returns the user ID if valid
func (a *Auth) ValidateSession(ctx context.Context, token string) (string, error) {
	var userID string
	var expiresAt time.Time

	err := a.db.QueryRowContext(ctx,
		"SELECT user_id, expires_at FROM sessions WHERE token = ?",
		token).Scan(&userID, &expiresAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("invalid session token")
		}
		return "", fmt.Errorf("failed to validate session: %w", err)
	}

	// Check if the session has expired
	if time.Now().After(expiresAt) {
		return "", errors.New("session expired")
	}

	return userID, nil
}

// DeleteSession removes a session from the database
func (a *Auth) DeleteSession(ctx context.Context, token string) error {
	_, err := a.db.ExecContext(ctx, "DELETE FROM sessions WHERE token = ?", token)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}
