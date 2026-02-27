package beads

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestNullStoreSatisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ Store = (*NullStore)(nil)
}

func TestNullStoreOperations(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ns := &NullStore{Logger: logger}
	ctx := context.Background()

	issues, err := ns.List(ctx, 10)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected empty list, got %d", len(issues))
	}

	issues, err = ns.Ready(ctx, 10)
	if err != nil {
		t.Fatalf("Ready error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected empty ready list, got %d", len(issues))
	}

	_, err = ns.Show(ctx, "issue-1")
	if err != nil {
		t.Fatalf("Show error: %v", err)
	}

	err = ns.Close(ctx, "issue-1", "done")
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}

	id, err := ns.Create(ctx, CreateParams{Title: "test"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id from NullStore.Create, got %q", id)
	}

	err = ns.Update(ctx, "issue-1", map[string]string{"status": "done"})
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}

	children, err := ns.Children(ctx, "parent-1")
	if err != nil {
		t.Fatalf("Children error: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("expected empty children, got %d", len(children))
	}
}
