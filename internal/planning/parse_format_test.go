package planning

import (
	"strings"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// --- parseApproaches tests ---

func TestParseApproaches_DirectArray(t *testing.T) {
	t.Parallel()
	input := `[{"title":"A","description":"desc A","tradeoffs":"pro A","confidence":0.9,"rank":1},
	           {"title":"B","description":"desc B","tradeoffs":"pro B","confidence":0.7,"rank":2}]`
	got, err := parseApproaches(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 approaches, got %d", len(got))
	}
	if got[0].Title != "A" || got[1].Title != "B" {
		t.Fatalf("wrong titles: %q, %q", got[0].Title, got[1].Title)
	}
	if got[0].Confidence != 0.9 {
		t.Fatalf("expected confidence 0.9, got %f", got[0].Confidence)
	}
}

func TestParseApproaches_WrappedObject(t *testing.T) {
	t.Parallel()
	input := `{"approaches":[{"title":"Wrapped","description":"d","tradeoffs":"t","confidence":0.5,"rank":1}]}`
	got, err := parseApproaches(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 approach, got %d", len(got))
	}
	if got[0].Title != "Wrapped" {
		t.Fatalf("expected title=Wrapped, got %q", got[0].Title)
	}
}

func TestParseApproaches_KeyedByRank(t *testing.T) {
	t.Parallel()
	input := `{"1":{"title":"First","description":"d","tradeoffs":"t","confidence":0.8,"rank":1},
	           "2":{"title":"Second","description":"d","tradeoffs":"t","confidence":0.6,"rank":2}}`
	got, err := parseApproaches(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 approaches, got %d", len(got))
	}
	// Keys are sorted, so "1" comes before "2"
	if got[0].Title != "First" {
		t.Fatalf("expected first title=First, got %q", got[0].Title)
	}
	if got[1].Title != "Second" {
		t.Fatalf("expected second title=Second, got %q", got[1].Title)
	}
}

func TestParseApproaches_EmptyArray(t *testing.T) {
	t.Parallel()
	_, err := parseApproaches(`[]`)
	if err == nil {
		t.Fatal("expected error for empty array")
	}
}

func TestParseApproaches_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := parseApproaches(`not json at all`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseApproaches_EmptyObject(t *testing.T) {
	t.Parallel()
	_, err := parseApproaches(`{}`)
	if err == nil {
		t.Fatal("expected error for empty object with no approach arrays")
	}
}

func TestParseApproaches_ObjectWithNonApproachValues(t *testing.T) {
	t.Parallel()
	// Object where values are strings, not approaches — should fail
	_, err := parseApproaches(`{"foo":"bar","baz":"qux"}`)
	if err == nil {
		t.Fatal("expected error for object with non-approach values")
	}
}

func TestParseApproaches_WrappedArbitraryKey(t *testing.T) {
	t.Parallel()
	// LLM might use any key name, not just "approaches"
	input := `{"results":[{"title":"Alt","description":"d","tradeoffs":"t","confidence":0.4,"rank":1}]}`
	got, err := parseApproaches(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Alt" {
		t.Fatalf("expected 1 approach titled Alt, got %v", got)
	}
}

func TestParseApproaches_SingleElementArray(t *testing.T) {
	t.Parallel()
	input := `[{"title":"Solo","description":"d","tradeoffs":"t","confidence":1.0,"rank":1}]`
	got, err := parseApproaches(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 approach, got %d", len(got))
	}
}

// --- formatApproachesSummary tests ---

func TestFormatApproachesSummary_Basic(t *testing.T) {
	t.Parallel()
	goal := ClarifiedGoal{Intent: "Build a widget", Why: "Users need it"}
	approaches := []ResearchedApproach{
		{Title: "Approach A", Description: "Do A", Tradeoffs: "Fast but risky", Confidence: 0.85, Rank: 1},
		{Title: "Approach B", Description: "Do B", Tradeoffs: "Slow but safe", Confidence: 0.6, Rank: 2},
	}
	result := formatApproachesSummary("sess-123", goal, approaches)

	checks := []string{
		"sess-123",
		"Build a widget",
		"Users need it",
		"2 approaches",
		"Approach A",
		"85%",
		"Approach B",
		"60%",
		"/plan select",
		"/plan dig",
		"/plan go",
	}
	for _, want := range checks {
		if !strings.Contains(result, want) {
			t.Errorf("expected summary to contain %q, got:\n%s", want, result)
		}
	}
}

func TestFormatApproachesSummary_NoWhy(t *testing.T) {
	t.Parallel()
	goal := ClarifiedGoal{Intent: "Do stuff"}
	result := formatApproachesSummary("s1", goal, nil)
	if strings.Contains(result, "Why:") {
		t.Error("expected no Why: line when Why is empty")
	}
}

func TestFormatApproachesSummary_EmptyApproaches(t *testing.T) {
	t.Parallel()
	goal := ClarifiedGoal{Intent: "Test"}
	result := formatApproachesSummary("s1", goal, nil)
	if !strings.Contains(result, "0 approaches") {
		t.Errorf("expected '0 approaches' in output, got:\n%s", result)
	}
}

// --- formatSingleApproach tests ---

func TestFormatSingleApproach(t *testing.T) {
	t.Parallel()
	a := ResearchedApproach{
		Title:       "Redis Cache",
		Description: "Use Redis for caching",
		Tradeoffs:   "External dependency",
		Confidence:  0.75,
		Rank:        2,
	}
	result := formatSingleApproach(a)

	checks := []string{"Approach 2", "Redis Cache", "75%", "Use Redis", "External dependency"}
	for _, want := range checks {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in output, got:\n%s", want, result)
		}
	}
}

func TestFormatSingleApproach_ZeroConfidence(t *testing.T) {
	t.Parallel()
	a := ResearchedApproach{Title: "Risky", Confidence: 0, Rank: 1}
	result := formatSingleApproach(a)
	if !strings.Contains(result, "0%") {
		t.Errorf("expected 0%% in output, got:\n%s", result)
	}
}

// --- formatDecompSummary tests ---

func TestFormatDecompSummary(t *testing.T) {
	t.Parallel()
	steps := []types.DecompStep{
		{Title: "Step One", Description: "Do first thing", Acceptance: "Tests pass", Estimate: 30},
		{Title: "Step Two", Description: "Do second thing", Acceptance: "Builds clean", Estimate: 60},
	}
	result := formatDecompSummary("My Approach", steps)

	checks := []string{
		"My Approach",
		"1. Step One",
		"~30m",
		"Tests pass",
		"2. Step Two",
		"~60m",
		"Builds clean",
	}
	for _, want := range checks {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in output, got:\n%s", want, result)
		}
	}
}

func TestFormatDecompSummary_Empty(t *testing.T) {
	t.Parallel()
	result := formatDecompSummary("Empty", nil)
	if !strings.Contains(result, "Empty") {
		t.Errorf("expected title in output, got:\n%s", result)
	}
	// With no steps, there should be no numbered items
	if strings.Contains(result, "1.") {
		t.Error("expected no numbered items for empty steps")
	}
}
