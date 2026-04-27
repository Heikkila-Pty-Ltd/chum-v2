package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.temporal.io/sdk/testsuite"
)

// ---------------------------------------------------------------------------
// mapResultType
// ---------------------------------------------------------------------------

func TestMapResultType_CompletedWithPR(t *testing.T) {
	got := mapResultType(CloseDetail{Reason: CloseCompleted, PRNumber: 42})
	if got != "code_change" {
		t.Fatalf("expected code_change, got %s", got)
	}
}

func TestMapResultType_CompletedWithoutPR(t *testing.T) {
	got := mapResultType(CloseDetail{Reason: CloseCompleted})
	if got != "research" {
		t.Fatalf("expected research, got %s", got)
	}
}

func TestMapResultType_DoDFailed(t *testing.T) {
	got := mapResultType(CloseDetail{Reason: CloseDoDFailed})
	if got != "report" {
		t.Fatalf("expected report, got %s", got)
	}
}

func TestMapResultType_NeedsReview(t *testing.T) {
	got := mapResultType(CloseDetail{Reason: CloseNeedsReview})
	if got != "report" {
		t.Fatalf("expected report, got %s", got)
	}
}

func TestMapResultType_Failed(t *testing.T) {
	got := mapResultType(CloseDetail{Reason: CloseFailed})
	if got != "error" {
		t.Fatalf("expected error, got %s", got)
	}
}

func TestMapResultType_Decomposed(t *testing.T) {
	got := mapResultType(CloseDetail{Reason: CloseDecomposed})
	if got != "report" {
		t.Fatalf("expected report, got %s", got)
	}
}

func TestMapResultType_Unknown(t *testing.T) {
	got := mapResultType(CloseDetail{Reason: CloseReason("unknown")})
	if got != "error" {
		t.Fatalf("expected error, got %s", got)
	}
}

// ---------------------------------------------------------------------------
// mapCallbackStatus
// ---------------------------------------------------------------------------

func TestMapCallbackStatus_Completed(t *testing.T) {
	if got := mapCallbackStatus(CloseCompleted); got != "success" {
		t.Fatalf("expected success, got %s", got)
	}
}

func TestMapCallbackStatus_DoDFailed(t *testing.T) {
	if got := mapCallbackStatus(CloseDoDFailed); got != "partial" {
		t.Fatalf("expected partial, got %s", got)
	}
}

func TestMapCallbackStatus_NeedsReview(t *testing.T) {
	if got := mapCallbackStatus(CloseNeedsReview); got != "partial" {
		t.Fatalf("expected partial, got %s", got)
	}
}

func TestMapCallbackStatus_Decomposed(t *testing.T) {
	if got := mapCallbackStatus(CloseDecomposed); got != "partial" {
		t.Fatalf("expected partial, got %s", got)
	}
}

func TestMapCallbackStatus_Failed(t *testing.T) {
	if got := mapCallbackStatus(CloseFailed); got != "failed" {
		t.Fatalf("expected failed, got %s", got)
	}
}

func TestMapCallbackStatus_Unknown(t *testing.T) {
	if got := mapCallbackStatus(CloseReason("bogus")); got != "failed" {
		t.Fatalf("expected failed, got %s", got)
	}
}

// ---------------------------------------------------------------------------
// callbackTitle
// ---------------------------------------------------------------------------

func TestCallbackTitle_WithSummary(t *testing.T) {
	got := callbackTitle(CloseDetail{Reason: CloseCompleted, Summary: "Custom summary"})
	if got != "Custom summary" {
		t.Fatalf("expected Custom summary, got %s", got)
	}
}

func TestCallbackTitle_CompletedWithPR(t *testing.T) {
	got := callbackTitle(CloseDetail{Reason: CloseCompleted, PRNumber: 7})
	if got != "CHUM completed: PR #7" {
		t.Fatalf("expected CHUM completed: PR #7, got %s", got)
	}
}

func TestCallbackTitle_CompletedWithoutPR(t *testing.T) {
	got := callbackTitle(CloseDetail{Reason: CloseCompleted})
	if got != "CHUM task completed" {
		t.Fatalf("expected CHUM task completed, got %s", got)
	}
}

func TestCallbackTitle_DoDFailed(t *testing.T) {
	got := callbackTitle(CloseDetail{Reason: CloseDoDFailed})
	if got != "CHUM task: DoD check failed" {
		t.Fatalf("expected CHUM task: DoD check failed, got %s", got)
	}
}

func TestCallbackTitle_Failed(t *testing.T) {
	got := callbackTitle(CloseDetail{Reason: CloseFailed})
	if got != "CHUM task failed" {
		t.Fatalf("expected CHUM task failed, got %s", got)
	}
}

func TestCallbackTitle_NeedsReview(t *testing.T) {
	got := callbackTitle(CloseDetail{Reason: CloseNeedsReview})
	if got != "CHUM task needs review" {
		t.Fatalf("expected CHUM task needs review, got %s", got)
	}
}

func TestCallbackTitle_Decomposed(t *testing.T) {
	got := callbackTitle(CloseDetail{Reason: CloseDecomposed})
	if got != "CHUM task decomposed into subtasks" {
		t.Fatalf("expected CHUM task decomposed into subtasks, got %s", got)
	}
}

func TestCallbackTitle_Default(t *testing.T) {
	got := callbackTitle(CloseDetail{Reason: CloseReason("other")})
	if got != "CHUM task result" {
		t.Fatalf("expected CHUM task result, got %s", got)
	}
}

// ---------------------------------------------------------------------------
// callbackBody
// ---------------------------------------------------------------------------

func TestCallbackBody_FullDetail(t *testing.T) {
	d := CloseDetail{
		Reason:    CloseCompleted,
		SubReason: "completed",
		PRNumber:  10,
		ReviewURL: "https://github.com/org/repo/pull/10",
		Category:  "test_failure",
		Summary:   "All tests passed",
	}
	body := callbackBody(d)
	for _, want := range []string{
		"**Status:** completed",
		"**Detail:** completed",
		"**PR:** #10",
		"**Review:** https://github.com/org/repo/pull/10",
		"**Category:** test_failure",
		"All tests passed",
	} {
		if !strContains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestCallbackBody_MinimalDetail(t *testing.T) {
	body := callbackBody(CloseDetail{Reason: CloseFailed})
	if !strContains(body, "**Status:** failed") {
		t.Fatalf("body missing status line:\n%s", body)
	}
	// Should NOT contain optional fields.
	for _, absent := range []string{"**Detail:**", "**PR:**", "**Review:**", "**Category:**"} {
		if strContains(body, absent) {
			t.Errorf("body should not contain %q:\n%s", absent, body)
		}
	}
}

func strContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && strHasSubstring(s, substr))
}

func strHasSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// validateCallbackURL
// ---------------------------------------------------------------------------

func TestValidateCallbackURL_ValidHTTP(t *testing.T) {
	if err := validateCallbackURL("http://localhost:3210/webhook"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateCallbackURL_ValidHTTPS(t *testing.T) {
	if err := validateCallbackURL("https://example.com/callback"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateCallbackURL_FileScheme(t *testing.T) {
	if err := validateCallbackURL("file:///etc/passwd"); err == nil {
		t.Fatal("expected error for file:// scheme")
	}
}

func TestValidateCallbackURL_FTPScheme(t *testing.T) {
	if err := validateCallbackURL("ftp://example.com/file"); err == nil {
		t.Fatal("expected error for ftp:// scheme")
	}
}

func TestValidateCallbackURL_NoScheme(t *testing.T) {
	if err := validateCallbackURL("example.com/callback"); err == nil {
		t.Fatal("expected error for missing scheme")
	}
}

// ---------------------------------------------------------------------------
// CallbackActivity (integration via Temporal test environment)
// ---------------------------------------------------------------------------

func TestCallbackActivity_EmptyURL(t *testing.T) {
	t.Parallel()
	a := &Activities{}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.CallbackActivity)

	_, err := env.ExecuteActivity(a.CallbackActivity, CallbackInput{
		URL:    "",
		TaskID: "t-1",
	})
	if err != nil {
		t.Fatalf("empty URL should be no-op, got %v", err)
	}
}

func TestCallbackActivity_InvalidScheme(t *testing.T) {
	t.Parallel()
	a := &Activities{}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.CallbackActivity)

	_, err := env.ExecuteActivity(a.CallbackActivity, CallbackInput{
		URL:    "ftp://evil.com/exfiltrate",
		TaskID: "t-1",
		Detail: CloseDetail{Reason: CloseCompleted},
	})
	if err != nil {
		t.Fatalf("invalid scheme should be no-op (non-fatal), got %v", err)
	}
}

func TestCallbackActivity_SuccessfulDelivery(t *testing.T) {
	t.Parallel()

	var received map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	a := &Activities{}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.CallbackActivity)

	_, err := env.ExecuteActivity(a.CallbackActivity, CallbackInput{
		URL:         srv.URL,
		ExternalRef: "ext-123",
		TaskID:      "task-42",
		Detail: CloseDetail{
			Reason:   CloseCompleted,
			PRNumber: 5,
		},
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify payload fields.
	if received["sourceItemId"] != "ext-123" {
		t.Errorf("sourceItemId = %v, want ext-123", received["sourceItemId"])
	}
	if received["workflowId"] != "task-42" {
		t.Errorf("workflowId = %v, want task-42", received["workflowId"])
	}
	if received["status"] != "success" {
		t.Errorf("status = %v, want success", received["status"])
	}
	if received["resultType"] != "code_change" {
		t.Errorf("resultType = %v, want code_change", received["resultType"])
	}
}

func TestCallbackActivity_RetryOn500(t *testing.T) {
	t.Parallel()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	a := &Activities{}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.CallbackActivity)

	_, err := env.ExecuteActivity(a.CallbackActivity, CallbackInput{
		URL:    srv.URL,
		TaskID: "t-retry",
		Detail: CloseDetail{Reason: CloseFailed},
	})
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestCallbackActivity_AllRetriesExhausted(t *testing.T) {
	t.Parallel()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	a := &Activities{}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.CallbackActivity)

	// Should NOT return an error — callback failures are best-effort.
	_, err := env.ExecuteActivity(a.CallbackActivity, CallbackInput{
		URL:    srv.URL,
		TaskID: "t-exhaust",
		Detail: CloseDetail{Reason: CloseFailed},
	})
	if err != nil {
		t.Fatalf("expected nil (best-effort), got %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}
