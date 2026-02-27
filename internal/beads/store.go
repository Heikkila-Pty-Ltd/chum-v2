package beads

import "context"

// Store abstracts beads issue operations for testability.
type Store interface {
	List(ctx context.Context, limit int) ([]Issue, error)
	Ready(ctx context.Context, limit int) ([]Issue, error)
	Show(ctx context.Context, issueID string) (Issue, error)
	Close(ctx context.Context, issueID, reason string) error
	Create(ctx context.Context, params CreateParams) (string, error)
	Update(ctx context.Context, issueID string, fields map[string]string) error
	Children(ctx context.Context, parentID string) ([]Issue, error)
}

// Verify Client implements Store at compile time.
var _ Store = (*Client)(nil)
