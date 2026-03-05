package jarvis

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
)

func TestAPISubmit(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger()}

	body, _ := json.Marshal(WorkRequest{
		Title:       "API test task",
		Description: "Test via API",
		Project:     "chum",
		Source:      "api-test",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/jarvis/submit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result map[string]string
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["task_id"] == "" {
		t.Error("expected non-empty task_id in response")
	}
}

func TestAPISubmit_BlockedByIngressPolicy(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger(), IngressPolicy: "beads_first"}

	body, _ := json.Marshal(WorkRequest{
		Title:       "API test task",
		Description: "Test via API",
		Project:     "chum",
		Source:      "api-test",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/jarvis/submit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

func TestAPISubmit_AllowedWithBeadsConfigured(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	e.ConfigureBeadsIngress("beads_only", "chum-canary", map[string]beads.Store{
		"chum": newStubBeadsStore(),
	})
	api := &API{Engine: e, Logger: testLogger(), IngressPolicy: "beads_only"}

	body, _ := json.Marshal(WorkRequest{
		Title:       "API beads task",
		Description: "Test via API beads ingress",
		Project:     "chum",
		Source:      "api-test",
		ExternalRef: "ext-api-1",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/jarvis/submit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result map[string]string
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	taskID := result["task_id"]
	if taskID == "" {
		t.Fatal("expected non-empty task_id")
	}
	task, err := d.GetTask(t.Context(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Metadata["external_ref"] != "ext-api-1" {
		t.Fatalf("metadata[external_ref] = %q, want ext-api-1", task.Metadata["external_ref"])
	}
}

func TestAPISubmit_BlockedByIngressPolicyEvenWithSystemHeader(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger(), IngressPolicy: "beads_only"}

	body, _ := json.Marshal(WorkRequest{
		Title:       "API test task",
		Description: "Test via API",
		Project:     "chum",
		Source:      "api-test",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/jarvis/submit", bytes.NewReader(body))
	req.Header.Set("X-CHUM-System-Caller", "true")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

func TestAPISubmitBadProject(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger()}

	body, _ := json.Marshal(WorkRequest{
		Title:   "Bad project",
		Project: "nope",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/jarvis/submit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestAPISubmitBadBody(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger()}

	req := httptest.NewRequest(http.MethodPost, "/api/jarvis/submit", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAPIStatus(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger()}

	// Submit a task first.
	id, err := e.Submit(t.Context(), WorkRequest{
		Title:   "Status test",
		Project: "chum",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jarvis/status/"+id, nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result WorkResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.TaskID != id {
		t.Errorf("task_id = %q, want %q", result.TaskID, id)
	}
	if result.Status != "ready" {
		t.Errorf("status = %q, want ready", result.Status)
	}
}

func TestAPIStatusNotFound(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger()}

	req := httptest.NewRequest(http.MethodGet, "/api/jarvis/status/nonexistent", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAPIPending(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger()}

	_, err := e.Submit(t.Context(), WorkRequest{
		Title:   "Pending test",
		Project: "chum",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jarvis/pending/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var results []WorkResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(results))
	}
}

func TestAPIHealth(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, Logger: testLogger()}

	req := httptest.NewRequest(http.MethodGet, "/api/jarvis/health", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
