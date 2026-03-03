package store

import (
	"testing"
)

func TestLessonStoreAndSearch(t *testing.T) {
	s := tempStore(t)

	// Store a lesson.
	id, err := s.StoreLesson(
		"chum-abc", "chum", "antipattern",
		"Always check error before using defer",
		"When calling os.Open, the error must be checked before deferring Close.",
		[]string{"internal/store/store.go", "internal/config/config.go"},
		[]string{"error-handling", "defer"},
	)
	if err != nil {
		t.Fatalf("StoreLesson: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	// Store second lesson.
	_, err = s.StoreLesson(
		"chum-def", "chum", "pattern",
		"Use context.WithTimeout for all external calls",
		"All CLI subprocess calls should use context.WithTimeout.",
		[]string{"internal/temporal/activities.go"},
		[]string{"timeout", "subprocess"},
	)
	if err != nil {
		t.Fatalf("StoreLesson 2: %v", err)
	}

	// Count.
	count, err := s.CountLessons("chum")
	if err != nil {
		t.Fatalf("CountLessons: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	// FTS5 search.
	results, err := s.SearchLessons("error handling defer", 10)
	if err != nil {
		t.Fatalf("SearchLessons: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 FTS5 result")
	}
	if results[0].Summary != "Always check error before using defer" {
		t.Fatalf("unexpected top result: %s", results[0].Summary)
	}

	// Search by file path.
	results, err = s.SearchLessonsByFilePath([]string{"internal/store/store.go"}, 10)
	if err != nil {
		t.Fatalf("SearchLessonsByFilePath: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for file path search")
	}

	// All-metachar query returns empty (not error).
	results, err = s.SearchLessons("(***)", 10)
	if err != nil {
		t.Fatalf("SearchLessons with metachar query: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for metachar query, got %d", len(results))
	}

	// Get by task.
	results, err = s.GetLessonsByTask("chum-abc")
	if err != nil {
		t.Fatalf("GetLessonsByTask: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 lesson for chum-abc, got %d", len(results))
	}
	if len(results[0].FilePaths) != 2 {
		t.Fatalf("expected 2 file paths, got %d", len(results[0].FilePaths))
	}
	if len(results[0].Labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(results[0].Labels))
	}

	// Get recent.
	results, err = s.GetRecentLessons("chum", 5)
	if err != nil {
		t.Fatalf("GetRecentLessons: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 recent lessons, got %d", len(results))
	}
}

func TestCountLessonsGlobal(t *testing.T) {
	s := tempStore(t)

	s.StoreLesson("m1", "proj-a", "pattern", "summary1", "detail1", nil, nil)
	s.StoreLesson("m2", "proj-b", "rule", "summary2", "detail2", nil, nil)

	count, _ := s.CountLessons("")
	if count != 2 {
		t.Fatalf("global count = %d, want 2", count)
	}

	count, _ = s.CountLessons("proj-a")
	if count != 1 {
		t.Fatalf("proj-a count = %d, want 1", count)
	}
}

func TestSearchLessonsEmptyPaths(t *testing.T) {
	s := tempStore(t)
	results, err := s.SearchLessonsByFilePath(nil, 10)
	if err != nil {
		t.Fatalf("SearchLessonsByFilePath nil: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for nil paths, got %d", len(results))
	}
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"uppercase operators preserved", "scope too-large OR underestimated",
			`"scope" "too-large" OR "underestimated"`},
		{"simple terms quoted", "error handling defer",
			`"error" "handling" "defer"`},
		{"AND NOT preserved", "foo AND bar NOT baz",
			`"foo" AND "bar" NOT "baz"`},
		{"empty string", "", ""},
		{"single term", "large", `"large"`},
		{"lowercase not is quoted", "not found", `"not" "found"`},
		{"lowercase or is quoted", "this or that", `"this" "or" "that"`},
		{"parentheses stripped", "fix error(s) in store",
			`"fix" "errors" "in" "store"`},
		{"asterisk stripped", "store*.go", `"store.go"`},
		{"all-special becomes empty", "(***)", ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
