package ast

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractKeywords(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   []string
	}{
		{
			name:   "simple task",
			prompt: "Add a rate limiter to the HTTP handler",
			want:   []string{"rate", "limiter", "http", "handler"},
		},
		{
			name:   "camelCase in prompt",
			prompt: "Fix buildCodebaseContext to filter relevant files",
			want:   []string{"buildcodebasecontext", "build", "codebase", "filter", "relevant", "files"},
		},
		{
			name:   "empty prompt",
			prompt: "",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractKeywords(tt.prompt)
			for _, w := range tt.want {
				assert.Contains(t, got, w, "missing keyword: %s", w)
			}
		})
	}
}

func TestFilterRelevant(t *testing.T) {
	files := []*ParsedFile{
		{
			Path:    "internal/auth/handler.go",
			Package: "auth",
			Symbols: []Symbol{
				{Name: "ValidateJWT", Kind: KindFunc, Signature: "func ValidateJWT(token string) (*Claims, error)"},
				{Name: "AuthMiddleware", Kind: KindFunc, Signature: "func AuthMiddleware(next http.Handler) http.Handler"},
			},
		},
		{
			Path:    "internal/dag/node.go",
			Package: "dag",
			Symbols: []Symbol{
				{Name: "Node", Kind: KindType, Signature: "type Node struct { ID string; Children []*Node }"},
				{Name: "AddChild", Kind: KindMethod, Receiver: "*Node", Signature: "func AddChild(child *Node)"},
			},
		},
		{
			Path:    "internal/engine/executor.go",
			Package: "engine",
			Symbols: []Symbol{
				{Name: "Execute", Kind: KindFunc, Signature: "func Execute(ctx context.Context, task Task) error"},
				{Name: "BuildPrompt", Kind: KindFunc, Signature: "func BuildPrompt(task Task, context string) string"},
			},
		},
		{
			Path:    "internal/config/config.go",
			Package: "config",
			Symbols: []Symbol{
				{Name: "Config", Kind: KindType, Signature: "type Config struct { DB string; Port int }"},
				{Name: "Load", Kind: KindFunc, Signature: "func Load(path string) (*Config, error)"},
			},
		},
	}

	t.Run("matches auth-related task", func(t *testing.T) {
		relevant, surrounding := FilterRelevant("Add JWT token validation to the auth middleware", files)
		require.NotEmpty(t, relevant)
		// Auth handler should be relevant
		var paths []string
		for _, f := range relevant {
			paths = append(paths, f.Path)
		}
		assert.Contains(t, paths, "internal/auth/handler.go")
		// Total files should be preserved
		assert.Equal(t, len(files), len(relevant)+len(surrounding))
	})

	t.Run("matches DAG-related task", func(t *testing.T) {
		relevant, surrounding := FilterRelevant("Add cycle detection to the DAG node graph", files)
		require.NotEmpty(t, relevant)
		var paths []string
		for _, f := range relevant {
			paths = append(paths, f.Path)
		}
		assert.Contains(t, paths, "internal/dag/node.go")
		assert.Equal(t, len(files), len(relevant)+len(surrounding))
	})

	t.Run("empty prompt returns all as surrounding", func(t *testing.T) {
		relevant, surrounding := FilterRelevant("", files)
		assert.Empty(t, relevant)
		assert.Equal(t, len(files), len(surrounding))
	})

	t.Run("no match returns all as surrounding", func(t *testing.T) {
		relevant, surrounding := FilterRelevant("kubernetes pod autoscaling", files)
		assert.Empty(t, relevant)
		assert.Equal(t, len(files), len(surrounding))
	})
}

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"buildCodebaseContext", []string{"build", "Codebase", "Context"}},
		{"simple", []string{"simple"}},
		{"HTTPHandler", []string{"H", "T", "T", "P", "Handler"}},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitCamelCase(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
