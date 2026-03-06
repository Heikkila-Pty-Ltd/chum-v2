package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

const userIDKey contextKey = "user_id"

// RequireAuth returns middleware that validates session tokens from cookies or
// Authorization headers. Valid requests get user_id set in the request context.
// Invalid/missing/expired tokens receive a 401 JSON response.
func RequireAuth(a *Auth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)
			if token == "" {
				writeUnauthorized(w)
				return
			}

			userID, err := a.ValidateSession(r.Context(), token)
			if err != nil {
				writeUnauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), userIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext retrieves the user_id from the request context.
// Returns empty string if not present.
func UserFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

func extractToken(r *http.Request) string {
	// Check cookie first
	if cookie, err := r.Cookie("session"); err == nil && cookie.Value != "" {
		return cookie.Value
	}

	// Fall back to Authorization: Bearer header
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	return ""
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"}); err != nil {
		// Best-effort response; write failures commonly mean the client disconnected.
		return
	}
}
