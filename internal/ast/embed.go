package ast

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	defaultOllamaURL  = "http://localhost:11434"
	defaultEmbedModel = "nomic-embed-text"
	// Timeout per batch — generous because large batches take time.
	embedTimeout = 120 * time.Second
	// Files with similarity above this threshold are considered relevant.
	similarityThreshold = 0.35
	// Maximum number of relevant files to return (cap for large codebases).
	maxRelevantFiles = 30
	// Maximum inputs per batch call to avoid OOM on huge codebases.
	embedBatchSize = 100
)

// EmbedFilter uses vector embeddings (via Ollama) to find files semantically
// relevant to a task prompt. Falls back to keyword-based FilterRelevant if
// the embedding service is unavailable.
type EmbedFilter struct {
	OllamaURL string
	Model     string
	client    *http.Client
}

// NewEmbedFilter creates an EmbedFilter with default settings.
func NewEmbedFilter() *EmbedFilter {
	return &EmbedFilter{
		OllamaURL: defaultOllamaURL,
		Model:     defaultEmbedModel,
		client: &http.Client{
			Timeout: embedTimeout,
		},
	}
}

// ollamaBatchEmbedRequest uses the /api/embed endpoint which accepts multiple
// inputs in a single call, returning all embeddings at once.
type ollamaBatchEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaBatchEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// Legacy single-input types kept for the single embed method.
type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float64 `json:"embedding"`
}

// embed calls ollama to get a vector embedding for a single text.
// Used for the task query embedding.
func (ef *EmbedFilter) embed(ctx context.Context, text string) ([]float64, error) {
	body, err := json.Marshal(ollamaEmbedRequest{
		Model:  ef.Model,
		Prompt: text,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ef.OllamaURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ef.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(result.Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding returned")
	}
	return result.Embedding, nil
}

// embedBatch calls ollama's /api/embed endpoint with multiple inputs at once.
// Returns one embedding per input in the same order.
func (ef *EmbedFilter) embedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(ollamaBatchEmbedRequest{
		Model: ef.Model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal batch embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ef.OllamaURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create batch embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ef.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama batch embed call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama batch returned status %d", resp.StatusCode)
	}

	var result ollamaBatchEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode batch embed response: %w", err)
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Embeddings))
	}
	return result.Embeddings, nil
}

// fileText builds a searchable text representation of a ParsedFile suitable
// for embedding. Includes path, package, symbol names, signatures, receivers,
// and doc comments.
func fileText(f *ParsedFile) string {
	var b strings.Builder
	b.WriteString(f.Path)
	b.WriteByte(' ')
	b.WriteString(f.Package)
	b.WriteByte(' ')
	for _, sym := range f.Symbols {
		b.WriteString(sym.Name)
		b.WriteByte(' ')
		b.WriteString(sym.Signature)
		b.WriteByte(' ')
		if sym.Receiver != "" {
			b.WriteString(sym.Receiver)
			b.WriteByte(' ')
		}
		if sym.DocComment != "" {
			b.WriteString(sym.DocComment)
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

type scoredFile struct {
	file  *ParsedFile
	score float64
}

// FilterRelevantByEmbedding uses vector similarity to split files into
// relevant and surrounding sets. Uses batch embedding for efficiency —
// one HTTP call for all files instead of N separate calls. Falls back
// to keyword-based FilterRelevant if embedding fails.
func (ef *EmbedFilter) FilterRelevantByEmbedding(ctx context.Context, taskPrompt string, files []*ParsedFile) (relevant, surrounding []*ParsedFile) {
	if len(files) == 0 || taskPrompt == "" {
		return nil, files
	}

	// Get task query embedding
	taskEmbedding, err := ef.embed(ctx, "search_query: "+taskPrompt)
	if err != nil {
		return FilterRelevant(taskPrompt, files)
	}

	// Separate files with symbols from empty ones
	type indexedFile struct {
		idx  int
		file *ParsedFile
	}
	var toEmbed []indexedFile
	for i, f := range files {
		if len(f.Symbols) == 0 && len(f.Imports) == 0 {
			surrounding = append(surrounding, f)
		} else {
			toEmbed = append(toEmbed, indexedFile{idx: i, file: f})
		}
	}

	if len(toEmbed) == 0 {
		return nil, surrounding
	}

	// Build batch input texts
	texts := make([]string, len(toEmbed))
	for i, tf := range toEmbed {
		texts[i] = "search_document: " + fileText(tf.file)
	}

	// Embed in batches
	allEmbeddings := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, err := ef.embedBatch(ctx, texts[start:end])
		if err != nil {
			// Batch failed — fall back to keyword filter
			return FilterRelevant(taskPrompt, files)
		}
		allEmbeddings = append(allEmbeddings, batch...)
	}

	// Score each file
	scored := make([]scoredFile, len(toEmbed))
	for i, emb := range allEmbeddings {
		scored[i] = scoredFile{
			file:  toEmbed[i].file,
			score: cosineSimilarity(taskEmbedding, emb),
		}
	}

	// Sort by similarity descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take files above threshold, up to maxRelevantFiles
	for _, sf := range scored {
		if sf.score >= similarityThreshold && len(relevant) < maxRelevantFiles {
			relevant = append(relevant, sf.file)
		} else {
			surrounding = append(surrounding, sf.file)
		}
	}

	// If nothing was relevant enough, fall back to keyword matching
	if len(relevant) == 0 {
		return FilterRelevant(taskPrompt, files)
	}

	return relevant, surrounding
}
