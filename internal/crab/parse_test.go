package crab

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMarkdownPlan_WellFormed(t *testing.T) {
	md := `# Plan: Refactor dispatch queue
## Context
The dispatch queue has grown unwieldy. We need to split it into
priority tiers and add backpressure.
## Scope
- [ ] Split queue into priority tiers
- [ ] Add backpressure mechanism
- [ ] Update dispatch metrics
## Acceptance Criteria
- All existing tests pass
- New priority queue benchmarks under 1ms p99
## Out of Scope
- UI changes
- API versioning
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Equal(t, "Refactor dispatch queue", plan.Title)
	require.Contains(t, plan.Context, "dispatch queue has grown unwieldy")
	require.Contains(t, plan.Context, "backpressure")
	require.Len(t, plan.ScopeItems, 3)

	require.Equal(t, 0, plan.ScopeItems[0].Index)
	require.Equal(t, "Split queue into priority tiers", plan.ScopeItems[0].Description)
	require.False(t, plan.ScopeItems[0].Completed)

	require.Equal(t, 1, plan.ScopeItems[1].Index)
	require.Equal(t, "Add backpressure mechanism", plan.ScopeItems[1].Description)

	require.Equal(t, 2, plan.ScopeItems[2].Index)
	require.Equal(t, "Update dispatch metrics", plan.ScopeItems[2].Description)

	require.Len(t, plan.AcceptanceCriteria, 2)
	require.Equal(t, "All existing tests pass", plan.AcceptanceCriteria[0])
	require.Equal(t, "New priority queue benchmarks under 1ms p99", plan.AcceptanceCriteria[1])

	require.Len(t, plan.OutOfScope, 2)
	require.Equal(t, "UI changes", plan.OutOfScope[0])
	require.Equal(t, "API versioning", plan.OutOfScope[1])

	require.Equal(t, md, plan.RawMarkdown)
}

func TestParseMarkdownPlan_CompletedScopeItems(t *testing.T) {
	md := `# Plan: Migration cleanup
## Context
Post-migration cleanup work.
## Scope
- [x] Remove legacy table
- [ ] Update ORM models
- [X] Archive old backups
## Acceptance Criteria
- No data loss
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Len(t, plan.ScopeItems, 3)

	require.True(t, plan.ScopeItems[0].Completed)
	require.Equal(t, "Remove legacy table", plan.ScopeItems[0].Description)

	require.False(t, plan.ScopeItems[1].Completed)
	require.Equal(t, "Update ORM models", plan.ScopeItems[1].Description)

	require.True(t, plan.ScopeItems[2].Completed)
	require.Equal(t, "Archive old backups", plan.ScopeItems[2].Description)
}

func TestParseMarkdownPlan_MissingSections(t *testing.T) {
	md := `# Plan: Minimal plan
## Context
Just the basics.
## Scope
- [ ] Do the thing
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Equal(t, "Minimal plan", plan.Title)
	require.Equal(t, "Just the basics.", plan.Context)
	require.Len(t, plan.ScopeItems, 1)
	require.Equal(t, "Do the thing", plan.ScopeItems[0].Description)
	require.Nil(t, plan.AcceptanceCriteria)
	require.Nil(t, plan.OutOfScope)
}

func TestParseMarkdownPlan_UnknownSectionsIgnored(t *testing.T) {
	md := `# Plan: With extras
## Context
Some context.
## Scope
- [ ] First deliverable
## Notes
This section should be completely ignored.
- Some note that is not a scope item
## Acceptance Criteria
- Tests pass
## References
- https://example.com
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Equal(t, "With extras", plan.Title)
	require.Len(t, plan.ScopeItems, 1)
	require.Len(t, plan.AcceptanceCriteria, 1)
	require.Equal(t, "Tests pass", plan.AcceptanceCriteria[0])
	require.Nil(t, plan.OutOfScope)
}

func TestParseMarkdownPlan_EmptyPlan(t *testing.T) {
	_, err := ParseMarkdownPlan("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no title")
}

func TestParseMarkdownPlan_NoScopeItems(t *testing.T) {
	md := `# Plan: Title only
## Context
Has context but no scope.
## Acceptance Criteria
- Something
`

	_, err := ParseMarkdownPlan(md)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no scope items")
}

func TestParseMarkdownPlan_AsteriskCheckboxVariant(t *testing.T) {
	md := `# Plan: Asterisk plan
## Context
Uses asterisk bullets.
## Scope
* [ ] First with asterisk
* [x] Second completed with asterisk
- [ ] Third with dash
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Len(t, plan.ScopeItems, 3)

	require.Equal(t, "First with asterisk", plan.ScopeItems[0].Description)
	require.False(t, plan.ScopeItems[0].Completed)
	require.Equal(t, 0, plan.ScopeItems[0].Index)

	require.Equal(t, "Second completed with asterisk", plan.ScopeItems[1].Description)
	require.True(t, plan.ScopeItems[1].Completed)
	require.Equal(t, 1, plan.ScopeItems[1].Index)

	require.Equal(t, "Third with dash", plan.ScopeItems[2].Description)
	require.False(t, plan.ScopeItems[2].Completed)
	require.Equal(t, 2, plan.ScopeItems[2].Index)
}

func TestParseMarkdownPlan_MultilineContext(t *testing.T) {
	md := `# Plan: Multiline context
## Context
First paragraph of context providing background
on the current situation.

Second paragraph with additional detail
that spans multiple lines as well.
## Scope
- [ ] Implement the feature
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Contains(t, plan.Context, "First paragraph")
	require.Contains(t, plan.Context, "Second paragraph")
	require.Contains(t, plan.Context, "\n\n")
}

func TestParseMarkdownPlan_TitleWhitespace(t *testing.T) {
	md := `# Plan:   Whitespace title
## Context
Some context.
## Scope
- [ ] A deliverable
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Equal(t, "Whitespace title", plan.Title)
}

func TestParseMarkdownPlan_TitleNoSpaceAfterColon(t *testing.T) {
	md := `# Plan:Compact title
## Scope
- [ ] Something
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Equal(t, "Compact title", plan.Title)
}

func TestParseMarkdownPlan_OnlyTitleNoScope(t *testing.T) {
	md := `# Plan: Just a title`
	_, err := ParseMarkdownPlan(md)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no scope items")
}

func TestParseMarkdownPlan_RawMarkdownPreserved(t *testing.T) {
	md := `# Plan: Raw check
## Scope
- [ ] Item one
`
	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Equal(t, md, plan.RawMarkdown)
}

func TestParseMarkdownPlan_EmptyContextSection(t *testing.T) {
	md := `# Plan: No context body
## Context
## Scope
- [ ] Deliverable
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	require.Equal(t, "", plan.Context)
}

func TestParseMarkdownPlan_ScopeItemsIndexZeroBased(t *testing.T) {
	md := `# Plan: Index check
## Scope
- [ ] Zero
- [ ] One
- [ ] Two
- [ ] Three
`

	plan, err := ParseMarkdownPlan(md)
	require.NoError(t, err)
	for i, item := range plan.ScopeItems {
		require.Equal(t, i, item.Index)
	}
}

func TestParseMarkdownPlan_WhitespaceOnlyInput(t *testing.T) {
	_, err := ParseMarkdownPlan("   \n  \n   ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no title")
}
