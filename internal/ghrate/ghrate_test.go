package ghrate

import (
	"context"
	"testing"
	"time"
)

func TestWaitAllowsImmediateCall(t *testing.T) {
	// The limiter has burst=5, so the first call should be immediate.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := Wait(ctx); err != nil {
		t.Fatalf("Wait returned error on first call: %v", err)
	}
}

func TestWaitRespectsContextCancellation(t *testing.T) {
	// Drain the burst tokens.
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = Wait(ctx)
	}

	// Now a call with an already-cancelled context should fail.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Wait(cancelled); err == nil {
		t.Fatal("Wait should return error with cancelled context")
	}
}
