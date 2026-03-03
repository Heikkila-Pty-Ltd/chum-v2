package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
)

// ProviderOption represents a fallback provider to try.
type ProviderOption struct {
	Agent string
	Model string
	Tier  string
}

// ProviderSelector returns the next provider to try after the current one
// has been exhausted. Returns nil if no more providers are available.
type ProviderSelector func(currentTier string) *ProviderOption

// RetryRunner wraps a Runner with retry logic and provider fallback.
// On transient errors (rate limits, overloaded), it retries with exponential
// backoff. If all retries for a provider are exhausted, it falls back to the
// next provider via the selector.
type RetryRunner struct {
	inner    Runner
	selector ProviderSelector
	logger   *slog.Logger

	// MaxRetries per provider (default 3).
	MaxRetries int
	// BaseDelay for exponential backoff (default 10s).
	BaseDelay time.Duration
	// MaxDelay caps the backoff (default 2m).
	MaxDelay time.Duration
	// MaxFallbacks limits how many provider escalations (default 2).
	MaxFallbacks int
}

// NewRetryRunner creates a RetryRunner wrapping the given Runner.
// selector may be nil if no provider fallback is desired.
func NewRetryRunner(inner Runner, selector ProviderSelector, logger *slog.Logger) *RetryRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &RetryRunner{
		inner:        inner,
		selector:     selector,
		logger:       logger,
		MaxRetries:   3,
		BaseDelay:    10 * time.Second,
		MaxDelay:     2 * time.Minute,
		MaxFallbacks: 2,
	}
}

func (r *RetryRunner) Plan(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error) {
	return r.runWithRetry(ctx, agent, model, workDir, prompt, "plan", func(ctx context.Context, a, m, w, p string) (*CLIResult, error) {
		return r.inner.Plan(ctx, a, m, w, p)
	})
}

func (r *RetryRunner) Exec(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error) {
	return r.runWithRetry(ctx, agent, model, workDir, prompt, "exec", func(ctx context.Context, a, m, w, p string) (*CLIResult, error) {
		return r.inner.Exec(ctx, a, m, w, p)
	})
}

type runFunc func(ctx context.Context, agent, model, workDir, prompt string) (*CLIResult, error)

func (r *RetryRunner) runWithRetry(ctx context.Context, agent, model, workDir, prompt, mode string, fn runFunc) (*CLIResult, error) {
	currentAgent := agent
	currentModel := model
	currentTier := "" // unknown initially, selector will figure it out
	maxRetries := r.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	var lastErr error

	for fallback := 0; fallback <= r.MaxFallbacks; fallback++ {
		for attempt := 1; attempt <= maxRetries; attempt++ {
			result, err := fn(ctx, currentAgent, currentModel, workDir, prompt)
			if err == nil {
				return result, nil
			}

			lastErr = err

			// Check if context is done (timeout, cancellation).
			if ctx.Err() != nil {
				return result, fmt.Errorf("context cancelled during %s retry: %w", mode, lastErr)
			}

			// Only retry on rate limit / transient errors.
			if !errors.Is(err, ErrRateLimited) && !isTransient(err) {
				return result, err
			}

			// Don't sleep on the last attempt before fallback.
			if attempt < maxRetries {
				delay := r.backoff(attempt)
				r.logger.Info("LLM rate limited, retrying",
					"mode", mode,
					"agent", currentAgent,
					"attempt", attempt,
					"maxRetries", maxRetries,
					"delay", delay.String(),
					"error", err.Error(),
				)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return nil, fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
				}
			}
		}

		// All retries exhausted for this provider. Try fallback.
		if r.selector == nil || fallback >= r.MaxFallbacks {
			break
		}

		next := r.selector(currentTier)
		if next == nil {
			r.logger.Info("No more fallback providers available",
				"lastAgent", currentAgent,
				"lastTier", currentTier,
			)
			break
		}

		r.logger.Info("Falling back to next provider",
			"fromAgent", currentAgent,
			"toAgent", next.Agent,
			"toModel", next.Model,
			"toTier", next.Tier,
		)
		currentAgent = next.Agent
		currentModel = next.Model
		currentTier = next.Tier
	}

	return nil, fmt.Errorf("all retries and fallbacks exhausted for %s: %w", mode, lastErr)
}

func (r *RetryRunner) backoff(attempt int) time.Duration {
	base := r.BaseDelay
	if base <= 0 {
		base = 10 * time.Second
	}
	maxD := r.MaxDelay
	if maxD <= 0 {
		maxD = 2 * time.Minute
	}
	delay := time.Duration(float64(base) * math.Pow(2, float64(attempt-1)))
	if delay > maxD {
		delay = maxD
	}
	return delay
}

// isTransient checks for transient HTTP-level errors that aren't caught by
// ErrRateLimited but are still worth retrying (e.g., 529 overloaded, 503, timeouts).
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	transientPatterns := []string{
		"529",
		"503",
		"502",
		"connection reset",
		"connection refused",
		"timeout",
		"temporary failure",
		"overloaded",
	}
	for _, p := range transientPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
