package planning

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestUIServer_ServesIndexHTML(t *testing.T) {
	t.Parallel()

	testFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!DOCTYPE html><html><body>CHUM UI</body></html>")},
		"style.css":  &fstest.MapFile{Data: []byte("body { margin: 0; }")},
	}

	srv := &UIServer{
		WebFS:  testFS,
		Logger: slog.Default(),
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/index.html")
	if err != nil {
		t.Fatalf("GET /index.html: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /index.html status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "CHUM UI") {
		t.Fatalf("GET /index.html body = %q, want to contain 'CHUM UI'", string(body))
	}
}

func TestUIServer_ServesStaticAssets(t *testing.T) {
	t.Parallel()

	testFS := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body { margin: 0; }")},
	}

	srv := &UIServer{
		WebFS:  testFS,
		Logger: slog.Default(),
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/style.css")
	if err != nil {
		t.Fatalf("GET /style.css: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /style.css status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "margin: 0") {
		t.Fatalf("GET /style.css body = %q, want to contain CSS", string(body))
	}
}

func TestUIServer_Returns404ForMissingFile(t *testing.T) {
	t.Parallel()

	testFS := fstest.MapFS{}

	srv := &UIServer{
		WebFS:  testFS,
		Logger: slog.Default(),
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/nonexistent.html")
	if err != nil {
		t.Fatalf("GET /nonexistent.html: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /nonexistent.html status = %d, want 404", resp.StatusCode)
	}
}
