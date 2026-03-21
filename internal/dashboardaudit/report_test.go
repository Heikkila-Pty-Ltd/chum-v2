package dashboardaudit

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestAnalyze_CapturesJarvisExclusiveAndSharedCode(t *testing.T) {
	t.Parallel()

	report, err := Analyze(repoRoot(t))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	jarvis := report.Views[viewJarvisKB]
	if !containsString(jarvis.CSSClassesExclusive, ".jv-goal-card") {
		t.Fatalf("jarvis CSS exclusives missing .jv-goal-card: %#v", jarvis.CSSClassesExclusive)
	}
	if containsString(jarvis.CSSClassesExclusive, ".jv-actions") {
		t.Fatalf(".jv-actions should be shared with overview, got exclusives %#v", jarvis.CSSClassesExclusive)
	}
	if !containsString(jarvis.JSFunctionsExclusive, "web/views/jarvis.js::renderGoals") {
		t.Fatalf("jarvis JS exclusives missing renderGoals: %#v", jarvis.JSFunctionsExclusive)
	}
	if !containsString(jarvis.JSFunctionsExclusive, "web/app.js::App.API.jarvisGoals") {
		t.Fatalf("jarvis JS exclusives missing App.API.jarvisGoals: %#v", jarvis.JSFunctionsExclusive)
	}
	if containsString(jarvis.JSFunctionsExclusive, "web/app.js::App.API.jarvisSummary") {
		t.Fatalf("App.API.jarvisSummary should be shared with overview, got exclusives %#v", jarvis.JSFunctionsExclusive)
	}

	if !containsString(report.BackendEndpointsExclusiveToJarvisKB, "GET /api/dashboard/jarvis/goals -> handleJarvisGoals (internal/jarvis/dashboard_jarvis.go)") {
		t.Fatalf("missing Jarvis KB goals endpoint: %#v", report.BackendEndpointsExclusiveToJarvisKB)
	}
	if !containsShared(report.SharedCodeDoNotDelete, "css_class", ".jv-actions") {
		t.Fatalf("missing shared css .jv-actions: %#v", report.SharedCodeDoNotDelete)
	}
	if !containsShared(report.SharedCodeDoNotDelete, "js_api", "web/app.js::App.API.jarvisSummary") {
		t.Fatalf("missing shared App.API.jarvisSummary: %#v", report.SharedCodeDoNotDelete)
	}
	if !containsSharedPrefix(report.SharedCodeDoNotDelete, "backend_endpoint", "GET /api/dashboard/jarvis/summary -> handleJarvisSummary") {
		t.Fatalf("missing shared backend jarvis summary endpoint: %#v", report.SharedCodeDoNotDelete)
	}
}

func TestAnalyze_CapturesTimelineAndStatsAsBackendOnly(t *testing.T) {
	t.Parallel()

	report, err := Analyze(repoRoot(t))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	timeline := report.Views[viewTimeline]
	if len(timeline.CSSClassesExclusive) != 0 {
		t.Fatalf("timeline CSS exclusives = %#v, want empty", timeline.CSSClassesExclusive)
	}
	if len(timeline.JSFunctionsExclusive) != 0 {
		t.Fatalf("timeline JS exclusives = %#v, want empty", timeline.JSFunctionsExclusive)
	}
	if !containsString(timeline.BackendEndpointsExclusive, "GET /api/dashboard/timeline/{project} -> handleDashboardTimeline (internal/jarvis/dashboard_api.go)") {
		t.Fatalf("timeline backend endpoint missing: %#v", timeline.BackendEndpointsExclusive)
	}
	if !containsNote(timeline.Notes, "backend route is still registered") {
		t.Fatalf("timeline note missing backend-only explanation: %#v", timeline.Notes)
	}

	stats := report.Views[viewStats]
	if len(stats.CSSClassesExclusive) != 0 {
		t.Fatalf("stats CSS exclusives = %#v, want empty", stats.CSSClassesExclusive)
	}
	if len(stats.JSFunctionsExclusive) != 0 {
		t.Fatalf("stats JS exclusives = %#v, want empty", stats.JSFunctionsExclusive)
	}
	if !containsString(stats.BackendEndpointsExclusive, "GET /api/dashboard/stats/{project} -> handleDashboardStats (internal/jarvis/dashboard_api.go)") {
		t.Fatalf("stats backend endpoint missing: %#v", stats.BackendEndpointsExclusive)
	}
	if !containsNote(stats.Notes, "backend route is still registered") {
		t.Fatalf("stats note missing backend-only explanation: %#v", stats.Notes)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsShared(values []SharedCode, kind, name string) bool {
	for _, value := range values {
		if value.Kind == kind && value.Name == name {
			return true
		}
	}
	return false
}

func containsSharedPrefix(values []SharedCode, kind, prefix string) bool {
	for _, value := range values {
		if value.Kind == kind && strings.HasPrefix(value.Name, prefix) {
			return true
		}
	}
	return false
}

func containsNote(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
