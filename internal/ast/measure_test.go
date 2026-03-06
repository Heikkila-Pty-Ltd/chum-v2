package ast

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMeasureFilterEfficiency(t *testing.T) {
	parser := NewParser(slog.Default())
	ctx := context.Background()
	embedServer := newDeterministicEmbedServer(t)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	files, err := parser.ParseDir(ctx, repoRoot)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	tasks := []string{
		"add rate limiting to the HTTP endpoint",
		"fix the user authentication login flow and JWT token validation",
		"add embedding-based filtering for AST parsed files",
		"fix profile registration and fitness scoring",
	}

	for _, task := range tasks {
		t.Run(task, func(t *testing.T) {
			// Full source = what you'd paste without AST
			fullSourceSize := 0
			for _, f := range files {
				fullSourceSize += len(f.DetailedSummary())
			}

			// AST signatures only = no filtering, just structural
			sigOnlySize := 0
			for _, f := range files {
				sigOnlySize += len(f.Summary())
			}

			// Keyword filter: relevant get full source, rest get signatures
			relKW, surKW := FilterRelevant(task, files)
			kwSize := 0
			for _, f := range relKW {
				kwSize += len(f.DetailedSummary())
			}
			for _, f := range surKW {
				kwSize += len(f.Summary())
			}

			// Embedding filter: semantic matching via deterministic local server.
			ef := NewEmbedFilter()
			ef.OllamaURL = embedServer.URL
			ef.client.Timeout = 2 * time.Second

			sample := files
			if len(sample) > 10 {
				sample = sample[:10]
			}
			relEmb, surEmb := ef.FilterRelevantByEmbedding(ctx, task, sample)
			embSize := 0
			for _, f := range relEmb {
				embSize += len(f.DetailedSummary())
			}
			for _, f := range surEmb {
				embSize += len(f.Summary())
			}

			fmt.Printf("\n=== Task: %s ===\n", task)
			fmt.Printf("Total files: %d\n", len(files))
			fmt.Printf("Full source (no AST):     %6d chars (~%5d tokens)\n", fullSourceSize, fullSourceSize/4)
			fmt.Printf("AST signatures only:      %6d chars (~%5d tokens) [%.0f%% saved]\n",
				sigOnlySize, sigOnlySize/4,
				100.0*(1.0-float64(sigOnlySize)/float64(fullSourceSize)))
			fmt.Printf("Keyword targeted:         %6d chars (~%5d tokens) [%.0f%% saved, %d relevant / %d surrounding]\n",
				kwSize, kwSize/4,
				100.0*(1.0-float64(kwSize)/float64(fullSourceSize)),
				len(relKW), len(surKW))
			fmt.Printf("Embed (10-file sample):   %6d chars (~%5d tokens) [%d relevant / %d surrounding]\n",
				embSize, embSize/4, len(relEmb), len(surEmb))
		})
	}
}

func newDeterministicEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embeddings":
			var req ollamaEmbedRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad embeddings request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(ollamaEmbedResponse{Embedding: deterministicEmbedding(req.Prompt)}); err != nil {
				http.Error(w, "encode embeddings response", http.StatusInternalServerError)
			}
		case "/api/embed":
			var req ollamaBatchEmbedRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad embed request", http.StatusBadRequest)
				return
			}
			embeddings := make([][]float64, 0, len(req.Input))
			for _, input := range req.Input {
				embeddings = append(embeddings, deterministicEmbedding(input))
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(ollamaBatchEmbedResponse{Embeddings: embeddings}); err != nil {
				http.Error(w, "encode embed response", http.StatusInternalServerError)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func deterministicEmbedding(text string) []float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(text)))
	sum := h.Sum64()
	return []float64{
		float64(sum&0xFFFF) / 65535.0,
		float64((sum>>16)&0xFFFF) / 65535.0,
		float64((sum>>32)&0xFFFF) / 65535.0,
		float64((sum>>48)&0xFFFF) / 65535.0,
	}
}
