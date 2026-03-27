package planning

import (
	"io/fs"
	"log/slog"
	"net/http"
)

// UIServer serves the embedded web UI as static files.
type UIServer struct {
	WebFS  fs.FS
	Logger *slog.Logger
}

// Handler returns an http.Handler that serves the embedded web assets.
func (s *UIServer) Handler() http.Handler {
	return http.FileServerFS(s.WebFS)
}
