package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

// mockRunner records calls and returns preset results.
type mockRunner struct {
	calls   []mockCall
	results []mockResult
	idx     int
}

type mockCall struct {
	agent, model, workDir, prompt string
	mode                          string // "plan" or "exec"
}

type mockResult struct {
	result *CLIResult
	err    error
}

func (m *mockRunner) Plan(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error) {
	m.calls = append(m.calls, mockCall{agent, model, workDir, prompt, "plan"})
	return m.next()
}

func (m *mockRunner) Exec(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error) {
	m.calls = append(m.calls, mockCall{agent, model, workDir, prompt, "exec"})
	return m.next()
}

func (m *mockRunner) next() (*CLIResult, error) {
	if m.idx >= len(m.results) {
		return nil, errors.New("no more mock results")
	}
	r := m.results[m.idx]
	m.idx++
	return r.result, r.err
}

func TestRetryRunner_SuccessOnFirstTry(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		results: []mockResult{
			{result: &CLIResult{Output: "ok"}, err: nil},
		},
	}
	r := NewRetryRunner(mock, nil, slog.Default())
	result, err := r.Exec(context.Background(), "claude", "", "/tmp", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "ok" {
		t.Fatalf("expected output 'ok', got %q", result.Output)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
}

func TestRetryRunner_RetriesOnRateLimit(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		results: []mockResult{
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
			{result: &CLIResult{Output: "ok"}, err: nil},
		},
	}
	r := NewRetryRunner(mock, nil, slog.Default())
	r.BaseDelay = 1 * time.Millisecond // fast tests
	r.MaxDelay = 10 * time.Millisecond
	result, err := r.Exec(context.Background(), "claude", "", "/tmp", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "ok" {
		t.Fatalf("expected output 'ok', got %q", result.Output)
	}
	if len(mock.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(mock.calls))
	}
}

func TestRetryRunner_NoRetryOnNonTransient(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		results: []mockResult{
			{result: nil, err: errors.New("PREFLIGHT: CLI not found")},
		},
	}
	r := NewRetryRunner(mock, nil, slog.Default())
	_, err := r.Exec(context.Background(), "claude", "", "/tmp", "test")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call (no retry), got %d", len(mock.calls))
	}
}

func TestRetryRunner_FallbackToNextProvider(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		results: []mockResult{
			// 3 failures on claude
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
			// success on gemini
			{result: &CLIResult{Output: "gemini-ok"}, err: nil},
		},
	}

	selector := func(currentTier string) *ProviderOption {
		return &ProviderOption{Agent: "gemini", Model: "gemini-pro", Tier: "balanced"}
	}

	r := NewRetryRunner(mock, selector, slog.Default())
	r.BaseDelay = 1 * time.Millisecond
	r.MaxDelay = 10 * time.Millisecond
	result, err := r.Plan(context.Background(), "claude", "", "/tmp", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "gemini-ok" {
		t.Fatalf("expected output 'gemini-ok', got %q", result.Output)
	}
	// Should have called claude 3 times, then gemini 1 time
	if len(mock.calls) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(mock.calls))
	}
	if mock.calls[3].agent != "gemini" {
		t.Fatalf("expected fallback to gemini, got %q", mock.calls[3].agent)
	}
	if mock.calls[3].model != "gemini-pro" {
		t.Fatalf("expected model gemini-pro, got %q", mock.calls[3].model)
	}
}

func TestRetryRunner_AllRetriesExhausted(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		results: []mockResult{
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
		},
	}
	r := NewRetryRunner(mock, nil, slog.Default())
	r.BaseDelay = 1 * time.Millisecond
	r.MaxDelay = 10 * time.Millisecond
	_, err := r.Exec(context.Background(), "claude", "", "/tmp", "test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited in chain, got: %v", err)
	}
}

func TestRetryRunner_ContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())

	mock := &mockRunner{
		results: []mockResult{
			{result: nil, err: fmt.Errorf("%w: claude", ErrRateLimited)},
		},
	}

	r := NewRetryRunner(mock, nil, slog.Default())
	r.BaseDelay = 1 * time.Second // long enough that cancel fires first

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := r.Exec(ctx, "claude", "", "/tmp", "test")
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
}

func TestRetryRunner_TransientErrorDetection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err       error
		transient bool
	}{
		{errors.New("HTTP 529 overloaded"), true},
		{errors.New("connection reset by peer"), true},
		{errors.New("503 service unavailable"), true},
		{errors.New("syntax error in code"), false},
		{errors.New("PREFLIGHT: CLI not found"), false},
		{nil, false},
	}
	for _, tt := range tests {
		got := isTransient(tt.err)
		if got != tt.transient {
			t.Errorf("isTransient(%v) = %v, want %v", tt.err, got, tt.transient)
		}
	}
}

func TestRetryRunner_SatisfiesRunnerInterface(t *testing.T) {
	t.Parallel()
	var _ Runner = &RetryRunner{}
}

func TestRetryRunner_BackoffProgression(t *testing.T) {
	t.Parallel()
	r := &RetryRunner{BaseDelay: 10 * time.Second, MaxDelay: 2 * time.Minute}
	d1 := r.backoff(1) // 10s
	d2 := r.backoff(2) // 20s
	d3 := r.backoff(3) // 40s

	if d1 != 10*time.Second {
		t.Errorf("backoff(1) = %v, want 10s", d1)
	}
	if d2 != 20*time.Second {
		t.Errorf("backoff(2) = %v, want 20s", d2)
	}
	if d3 != 40*time.Second {
		t.Errorf("backoff(3) = %v, want 40s", d3)
	}

	// Should cap at MaxDelay
	d10 := r.backoff(10)
	if d10 > 2*time.Minute {
		t.Errorf("backoff(10) = %v, should be capped at 2m", d10)
	}
}
