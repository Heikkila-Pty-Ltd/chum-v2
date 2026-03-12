package jarvis

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
)

func testPlanAPI(t *testing.T) (*API, *dag.DAG) {
	t.Helper()
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, DAG: d, LLM: llm.CLIRunner{}, Logger: testLogger()}
	return api, d
}

func TestHandlePlanCreate(t *testing.T) {
	api, _ := testPlanAPI(t)

	body := `{"project":"chum","title":"Auth Feature","brief":"Add OAuth2 login"}`
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/plans", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.ID == "" {
		t.Fatal("expected non-empty plan ID")
	}
}

func TestHandlePlanCreate_NoBrief(t *testing.T) {
	api, _ := testPlanAPI(t)

	body := `{"project":"chum","title":"Simple Plan"}`
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/plans", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

func TestHandlePlanList(t *testing.T) {
	api, d := testPlanAPI(t)

	// Create two plans.
	for _, title := range []string{"Plan A", "Plan B"} {
		p := &dag.PlanDoc{Project: "chum", Title: title}
		if err := d.CreatePlan(t.Context(), p); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/plans/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		Plans []dag.PlanDocSummary `json:"plans"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Plans) != 2 {
		t.Fatalf("got %d plans, want 2", len(result.Plans))
	}
}

func TestHandlePlanList_Empty(t *testing.T) {
	api, _ := testPlanAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/plans/nonexistent", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var result struct {
		Plans []dag.PlanDocSummary `json:"plans"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Plans) != 0 {
		t.Fatalf("got %d plans, want 0", len(result.Plans))
	}
}

func TestHandlePlanGet(t *testing.T) {
	api, d := testPlanAPI(t)

	p := &dag.PlanDoc{Project: "chum", Title: "Get Test"}
	if err := d.CreatePlan(t.Context(), p); err != nil {
		t.Fatalf("create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/plan/"+p.ID, nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result dag.PlanDoc
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Title != "Get Test" {
		t.Fatalf("title = %q, want %q", result.Title, "Get Test")
	}
}

func TestHandlePlanGet_NotFound(t *testing.T) {
	api, _ := testPlanAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/plan/nonexistent", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
