package beads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if err := c.AddDependency(ctx, "test-123", "test-456"); err == nil {
		t.Error("AddDependency should fail on read-only client")
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

func TestList_UsesAllFlag(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	bin := filepath.Join(dir, "fake-bd.sh")
	script := "#!/bin/sh\n" +
		"echo \"$*\" > \"" + argsFile + "\"\n" +
		"echo '[]'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	orig := DefaultBinary
	DefaultBinary = bin
	defer func() { DefaultBinary = orig }()

	c, err := NewClient(dir)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.List(context.Background(), 0); err != nil {
		t.Fatalf("List: %v", err)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := strings.TrimSpace(string(b))
	if got != "list --all --json" {
		t.Fatalf("unexpected args: %q", got)
	}
}
