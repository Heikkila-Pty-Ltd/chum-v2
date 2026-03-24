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

func TestIsExclusiveToView_UsesObservedUsageOnly(t *testing.T) {
	t.Parallel()

	if !isExclusiveToView("jv-goal-card", map[string]bool{viewJarvisKB: true}, viewJarvisKB) {
		t.Fatal("expected single-view Jarvis usage to be exclusive")
	}
	if isExclusiveToView("jv-goal-card", map[string]bool{"overview": true}, viewJarvisKB) {
		t.Fatal("expected overview-only usage to prevent Jarvis exclusivity")
	}
	if isExclusiveToView("jv-goal-card", map[string]bool{viewJarvisKB: true, "overview": true}, viewJarvisKB) {
		t.Fatal("expected multi-view usage to prevent exclusivity")
	}
	if isExclusiveToView("jv-goal-card", nil, viewJarvisKB) {
		t.Fatal("expected classes without observed usage to stay non-exclusive")
	}
}

func TestExtractCSSClasses_OnlyReadsSelectorClasses(t *testing.T) {
	t.Parallel()

	files := []frontendFile{
		{
			RelPath: "web/style.css",
			Content: `
.real-class, button.primary:hover, .compound.foo { opacity: .5; transform: translate(.4rem, 0); }
.rule { background-image: url("/assets/icon.svg"); }
@media screen and (min-width: 800px) { .nested-class { color: red; } }
`,
		},
	}

	classes := extractCSSClasses(files)

	for _, want := range []string{"real-class", "primary", "compound", "foo", "rule", "nested-class"} {
		if !classes[want] {
			t.Fatalf("missing class %q in %#v", want, classes)
		}
	}
	for _, unexpected := range []string{"5", "4rem", "svg"} {
		if classes[unexpected] {
			t.Fatalf("unexpected non-selector token %q in %#v", unexpected, classes)
		}
	}
}

func TestExtractAPIMethods_AllowsMultilineMethodEntries(t *testing.T) {
	t.Parallel()

	methods, err := extractAPIMethods([]frontendFile{
		{
			RelPath: "web/app.js",
			Content: `
const App = (() => {
  const API = {
    async get(path) { return path; },
    async post(path, body) { return { path, body }; },
    jarvisGoals:
      () =>
        API.get(
          '/api/dashboard/jarvis/goals'
        ),
    jarvisResolve: (body) =>
      API.post(
        "/api/dashboard/jarvis/actions/resolve",
        body,
      ),
  };
})();
`,
		},
	})
	if err != nil {
		t.Fatalf("extractAPIMethods() error = %v", err)
	}

	if got := methods["jarvisGoals"]; got.Route != "/api/dashboard/jarvis/goals" || got.HTTPMethod != "GET" {
		t.Fatalf("jarvisGoals = %#v", got)
	}
	if got := methods["jarvisResolve"]; got.Route != "/api/dashboard/jarvis/actions/resolve" || got.HTTPMethod != "POST" {
		t.Fatalf("jarvisResolve = %#v", got)
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
