package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
)

type HealthResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Server struct {
	cfg    *config.Config
	logger *slog.Logger
}

func NewServer(cfg *config.Config, logger *slog.Logger) *Server {
	return &Server{
		cfg:    cfg,
		logger: logger,
	}
}

func (s *Server) Start(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	s.logger.Info("Starting health server", "addr", addr)
	err := server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Check Temporal connectivity
	c, err := client.Dial(client.Options{
		HostPort:  s.cfg.General.TemporalHostPort,
		Namespace: s.cfg.General.TemporalNamespace,
	})
	if err != nil {
		s.respondUnhealthy(w, "Failed to connect to Temporal: "+err.Error())
		return
	}
	defer c.Close()

	// Test Temporal connection with a simple operation
	_, err = c.WorkflowService().GetSystemInfo(ctx, &workflowservice.GetSystemInfoRequest{})
	if err != nil {
		s.respondUnhealthy(w, "Temporal connectivity check failed: "+err.Error())
		return
	}

	// All checks passed
	response := HealthResponse{Status: "ok"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) respondUnhealthy(w http.ResponseWriter, errorMsg string) {
	response := HealthResponse{
		Status: "unhealthy",
		Error:  errorMsg,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(response)
}