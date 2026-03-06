package ast

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors
	assert.InDelta(t, 1.0, cosineSimilarity([]float64{1, 0, 0}, []float64{1, 0, 0}), 0.001)

	// Orthogonal vectors
	assert.InDelta(t, 0.0, cosineSimilarity([]float64{1, 0, 0}, []float64{0, 1, 0}), 0.001)

	// Opposite vectors
	assert.InDelta(t, -1.0, cosineSimilarity([]float64{1, 0, 0}, []float64{-1, 0, 0}), 0.001)

	// Same direction, different magnitude
	assert.InDelta(t, 1.0, cosineSimilarity([]float64{1, 2, 3}, []float64{2, 4, 6}), 0.001)

	// Empty/mismatched vectors
	assert.Equal(t, 0.0, cosineSimilarity(nil, nil))
	assert.Equal(t, 0.0, cosineSimilarity([]float64{1}, []float64{1, 2}))
	assert.Equal(t, 0.0, cosineSimilarity([]float64{0, 0}, []float64{0, 0}))
}

func TestFileText(t *testing.T) {
	f := &ParsedFile{
		Path:    "internal/engine/activities.go",
		Package: "engine",
		Symbols: []Symbol{
			{Name: "BuildContext", Signature: "func BuildContext() string", Receiver: "Activities", DocComment: "builds codebase context"},
			{Name: "Execute", Signature: "func Execute(task string) error"},
		},
	}
	text := fileText(f)
	assert.Contains(t, text, "internal/engine/activities.go")
	assert.Contains(t, text, "engine")
	assert.Contains(t, text, "BuildContext")
	assert.Contains(t, text, "Activities")
	assert.Contains(t, text, "builds codebase context")
	assert.Contains(t, text, "Execute")
}

func TestFilterRelevantByEmbedding_EmptyInputs(t *testing.T) {
	ef := NewEmbedFilter()
	ctx := context.Background()

	// Empty files
	rel, sur := ef.FilterRelevantByEmbedding(ctx, "some task", nil)
	assert.Nil(t, rel)
	assert.Nil(t, sur)

	// Empty prompt
	files := []*ParsedFile{{Path: "test.go", Package: "main"}}
	rel, sur = ef.FilterRelevantByEmbedding(ctx, "", files)
	assert.Nil(t, rel)
	assert.Equal(t, files, sur)
}

func TestFilterRelevantByEmbedding_FallbackOnBadURL(t *testing.T) {
	ef := NewEmbedFilter()
	ef.OllamaURL = "http://localhost:99999" // Bad port, will fail
	ctx := context.Background()

	files := []*ParsedFile{
		{Path: "auth/login.go", Package: "auth", Symbols: []Symbol{{Name: "Login", Signature: "func Login(user string) error"}}},
		{Path: "db/store.go", Package: "db", Symbols: []Symbol{{Name: "Store", Signature: "func Store(data []byte) error"}}},
	}

	// Should fall back to keyword filter
	rel, sur := ef.FilterRelevantByEmbedding(ctx, "login authentication user", files)
	// Keyword filter should find "login" and "auth"
	assert.NotNil(t, rel)
	assert.NotNil(t, sur)
}

// TestFilterRelevantByEmbedding_Integration tests against a live ollama instance.
// Skipped if ollama is not available.
func TestFilterRelevantByEmbedding_Integration(t *testing.T) {
	if os.Getenv("CHUM_AST_RUN_LIVE_EMBED_TESTS") != "1" {
		t.Skip("set CHUM_AST_RUN_LIVE_EMBED_TESTS=1 to run live embedding integration tests")
	}

	ef := NewEmbedFilter()
	ctx := context.Background()

	// Quick check if ollama is up
	_, err := ef.embed(ctx, "test")
	if err != nil {
		t.Skip("ollama not available, skipping integration test")
	}

	files := []*ParsedFile{
		{
			Path:    "internal/auth/handler.go",
			Package: "auth",
			Symbols: []Symbol{
				{Name: "HandleLogin", Signature: "func HandleLogin(w http.ResponseWriter, r *http.Request)", DocComment: "handles user authentication login requests"},
				{Name: "ValidateToken", Signature: "func ValidateToken(token string) (*Claims, error)", DocComment: "validates JWT token and returns claims"},
			},
		},
		{
			Path:    "internal/db/migrations.go",
			Package: "db",
			Symbols: []Symbol{
				{Name: "RunMigrations", Signature: "func RunMigrations(db *sql.DB) error", DocComment: "applies pending database schema migrations"},
				{Name: "MigrationVersion", Signature: "func MigrationVersion(db *sql.DB) (int, error)"},
			},
		},
		{
			Path:    "internal/engine/worker.go",
			Package: "engine",
			Symbols: []Symbol{
				{Name: "StartWorker", Signature: "func StartWorker(ctx context.Context) error", DocComment: "starts the temporal workflow worker"},
				{Name: "ProcessTask", Signature: "func ProcessTask(task *Task) (*Result, error)", DocComment: "executes a task and returns the result"},
			},
		},
		{
			Path:    "internal/auth/middleware.go",
			Package: "auth",
			Symbols: []Symbol{
				{Name: "AuthMiddleware", Signature: "func AuthMiddleware(next http.Handler) http.Handler", DocComment: "HTTP middleware that checks authentication"},
				{Name: "RequireRole", Signature: "func RequireRole(role string) func(http.Handler) http.Handler", DocComment: "requires the user to have a specific role"},
			},
		},
	}

	// Search for auth-related content
	rel, sur := ef.FilterRelevantByEmbedding(ctx, "fix the user authentication login flow and token validation", files)

	// We expect auth files to score higher than db/engine files
	require.NotEmpty(t, rel, "should find at least one relevant file")

	relPaths := make(map[string]bool)
	for _, f := range rel {
		relPaths[f.Path] = true
	}

	// Auth files should be relevant
	assert.True(t, relPaths["internal/auth/handler.go"], "auth handler should be relevant")

	// DB migrations should NOT be relevant
	surPaths := make(map[string]bool)
	for _, f := range sur {
		surPaths[f.Path] = true
	}
	assert.True(t, surPaths["internal/db/migrations.go"], "db migrations should be surrounding, not relevant")

	t.Logf("Relevant files: %d, Surrounding files: %d", len(rel), len(sur))
	for _, f := range rel {
		t.Logf("  RELEVANT: %s", f.Path)
	}
	for _, f := range sur {
		t.Logf("  SURROUNDING: %s", f.Path)
	}
}

func TestCosineSimilarity_Normalized(t *testing.T) {
	// Real-world: embeddings are typically normalized, so cosine = dot product
	a := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	norm := math.Sqrt(0.01 + 0.04 + 0.09 + 0.16 + 0.25)
	for i := range a {
		a[i] /= norm
	}
	// Self-similarity of normalized vector should be 1.0
	assert.InDelta(t, 1.0, cosineSimilarity(a, a), 0.0001)
}
