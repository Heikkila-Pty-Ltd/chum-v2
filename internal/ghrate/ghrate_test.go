package ghrate

import (
	"context"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestWaitAllowsImmediateCall(t *testing.T) {
	// Use a local limiter so tests are independent of shared state.
	l := rate.NewLimiter(rate.Limit(0.9), 5)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error on first call: %v", err)
	}
}

func TestWaitRespectsContextCancellation(t *testing.T) {
	// Use a local limiter and drain it.
	l := rate.NewLimiter(rate.Limit(0.9), 5)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = l.Wait(ctx)
	}

	// A call with an already-cancelled context should fail immediately.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Wait(cancelled); err == nil {
		t.Fatal("Wait should return error with cancelled context")
	}
}
