package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrUserExists is returned when trying to create a user that already exists
	ErrUserExists = errors.New("user already exists")
	// ErrUserNotFound is returned when a user is not found
	ErrUserNotFound = errors.New("user not found")
	// ErrSessionNotFound is returned when a session is not found
	ErrSessionNotFound = errors.New("session not found")
)

// User represents a user in the system
type User struct {
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// Session represents a user session
type Session struct {
	Token     string
	Username  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Auth handles authentication operations
type Auth struct {
	db *sql.DB
}

// New creates a new Auth instance
func New(db *sql.DB) *Auth {
	return &Auth{db: db}
}

// EnsureSchema creates the necessary database tables
func (a *Auth) EnsureSchema() error {
	usersTable := `
		CREATE TABLE IF NOT EXISTS users (
			username TEXT PRIMARY KEY,
			password_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`

	sessionsTable := `
		CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (username) REFERENCES users(username) ON DELETE CASCADE
		)
	`

	if _, err := a.db.Exec(usersTable); err != nil {
		return fmt.Errorf("failed to create users table: %w", err)
	}

	if _, err := a.db.Exec(sessionsTable); err != nil {
		return fmt.Errorf("failed to create sessions table: %w", err)
	}

	return nil
}

// CreateUser creates a new user with the given username and password
func (a *Auth) CreateUser(username, password string) error {
	// Check if user already exists
	var existingUser string
	err := a.db.QueryRow("SELECT username FROM users WHERE username = ?", username).Scan(&existingUser)
	if err == nil {
		return ErrUserExists
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("failed to check existing user: %w", err)
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Insert user
	_, err = a.db.Exec(
		"INSERT INTO users (username, password_hash) VALUES (?, ?)",
		username, string(hashedPassword),
	)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// GetUser retrieves a user by username
func (a *Auth) GetUser(username string) (*User, error) {
	var user User
	err := a.db.QueryRow(
		"SELECT username, password_hash, created_at FROM users WHERE username = ?",
		username,
	).Scan(&user.Username, &user.PasswordHash, &user.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

// CreateSession creates a new session for the given user
func (a *Auth) CreateSession(username string, expiresAt time.Time) (string, error) {
	// Generate random token
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	// Insert session
	_, err = a.db.Exec(
		"INSERT INTO sessions (token, username, expires_at) VALUES (?, ?, ?)",
		token, username, expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return token, nil
}

// DeleteSession deletes a session by token
func (a *Auth) DeleteSession(token string) error {
	_, err := a.db.Exec("DELETE FROM sessions WHERE token = ?", token)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by token
func (a *Auth) GetSession(token string) (*Session, error) {
	var session Session
	err := a.db.QueryRow(
		"SELECT token, username, expires_at, created_at FROM sessions WHERE token = ? AND expires_at > ?",
		token, time.Now(),
	).Scan(&session.Token, &session.Username, &session.ExpiresAt, &session.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return &session, nil
}

// generateToken generates a cryptographically secure random token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}