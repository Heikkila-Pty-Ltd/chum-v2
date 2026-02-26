package beads

import (
	"context"
	"testing"
)

func TestNewReadOnlyClient_BlocksWrites(t *testing.T) {
	// Use a fake binary that exists on PATH so NewReadOnlyClient succeeds.
	orig := DefaultBinary
	DefaultBinary = "true" // /usr/bin/true
	defer func() { DefaultBinary = orig }()

	c, err := NewReadOnlyClient(t.TempDir())
	if err != nil {
		t.Fatalf("NewReadOnlyClient: %v", err)
	}

	ctx := context.Background()
	if err := c.Close(ctx, "test-123", "done"); err == nil {
		t.Error("Close should fail on read-only client")
	}
	if err := c.Update(ctx, "test-123", map[string]string{"status": "done"}); err == nil {
		t.Error("Update should fail on read-only client")
	}
}

func TestUpdate_UnsupportedField(t *testing.T) {
	orig := DefaultBinary
	DefaultBinary = "true"
	defer func() { DefaultBinary = orig }()

	c, err := NewClient(t.TempDir())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := context.Background()
	err = c.Update(ctx, "test-123", map[string]string{"bogus_field": "value"})
	if err == nil {
		t.Error("Update with unsupported field should fail")
	}
}
