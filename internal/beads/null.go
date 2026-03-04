package beads

import (
	"context"
	"log/slog"
)

// NullStore is a guard object that satisfies Store but does nothing.
// Used when the bd CLI is unavailable for a project.
type NullStore struct {
	Logger *slog.Logger
}

// Verify NullStore implements Store at compile time.
var _ Store = (*NullStore)(nil)

func (n *NullStore) List(_ context.Context, _ int) ([]Issue, error) {
	return nil, nil
}

func (n *NullStore) Ready(_ context.Context, _ int) ([]Issue, error) {
	return nil, nil
}

// Show returns a stub Issue with only the ID populated.
// Callers should not rely on other fields being set.
func (n *NullStore) Show(_ context.Context, issueID string) (Issue, error) {
	return Issue{ID: issueID}, nil
}

func (n *NullStore) Close(_ context.Context, _, _ string) error {
	return nil
}

func (n *NullStore) Create(_ context.Context, _ CreateParams) (string, error) {
	return "", nil
}

func (n *NullStore) Update(_ context.Context, _ string, _ map[string]string) error {
	return nil
}

func (n *NullStore) Children(_ context.Context, _ string) ([]Issue, error) {
	return nil, nil
}

func (n *NullStore) AddDependency(_ context.Context, _, _ string) error {
	return nil
}
