// Package ghrate provides a shared rate limiter for GitHub API calls.
//
// GitHub allows 5,000 authenticated requests per hour. This limiter
// targets ~80% of that budget (~4,000/hr ≈ 1.1 req/sec) so concurrent
// agent workflows don't exhaust the quota.
package ghrate

import (
	"context"

	"golang.org/x/time/rate"
)

// limiter is the process-wide GitHub API rate limiter.
// 0.9 tokens/sec ≈ 3,240/hr, well within the 5,000/hr budget.
// Burst of 5 allows short bursts of sequential calls (e.g. review
// submit + read-back) without unnecessary blocking.
var limiter = rate.NewLimiter(rate.Limit(0.9), 5)

// Wait blocks until the rate limiter allows one GitHub API call.
// Returns immediately if the limiter has available tokens.
// Returns ctx.Err() if the context is cancelled while waiting.
func Wait(ctx context.Context) error {
	return limiter.Wait(ctx)
}
