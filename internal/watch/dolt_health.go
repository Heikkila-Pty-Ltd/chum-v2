// Package watch provides monitoring workflows for CHUM infrastructure.
package watch

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"syscall"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/notify"
)

// DoltHealthConfig holds settings for the Dolt health check schedule.
type DoltHealthConfig struct {
	DoltDataDir string `json:"dolt_data_dir"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	MaxRestarts int    `json:"max_restarts"`
	AlertRoomID string `json:"alert_room_id"`
}

// DoltHealthCheckWorkflow is a Temporal scheduled workflow that checks
// Dolt connectivity and restarts the server if it's down.
func DoltHealthCheckWorkflow(ctx workflow.Context, cfg DoltHealthConfig) error {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 3307
	}
	if cfg.MaxRestarts == 0 {
		cfg.MaxRestarts = 3
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Check health
	var healthy bool
	err := workflow.ExecuteActivity(ctx, CheckDoltHealthActivity, cfg.Host, cfg.Port).Get(ctx, &healthy)
	if err != nil {
		return fmt.Errorf("health check activity: %w", err)
	}
	if healthy {
		return nil
	}

	// Unhealthy — attempt restart
	for attempt := 1; attempt <= cfg.MaxRestarts; attempt++ {
		err = workflow.ExecuteActivity(ctx, RestartDoltActivity, cfg.DoltDataDir, cfg.Host, cfg.Port).Get(ctx, nil)
		if err == nil {
			return nil
		}
	}

	// All restart attempts failed — alert if configured
	if cfg.AlertRoomID != "" {
		alertAO := workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
		}
		alertCtx := workflow.WithActivityOptions(ctx, alertAO)
		msg := fmt.Sprintf("Dolt health check: %d restart attempts failed for %s:%d", cfg.MaxRestarts, cfg.Host, cfg.Port)
		_ = workflow.ExecuteActivity(alertCtx, "AlertDoltFailureActivity", cfg.AlertRoomID, msg).Get(alertCtx, nil)
	}

	return fmt.Errorf("dolt unrecoverable after %d restart attempts", cfg.MaxRestarts)
}

// DoltHealthActivities holds dependencies for health check activities.
type DoltHealthActivities struct {
	Logger   *slog.Logger
	ChatSend notify.ChatSender
}

// CheckDoltHealthActivity tests TCP connectivity to Dolt.
func CheckDoltHealthActivity(_ context.Context, host string, port int) (bool, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false, nil
	}
	conn.Close()
	return true, nil
}

// RestartDoltActivity kills the Dolt server bound to the specified port
// and starts a new one from the data directory.
func RestartDoltActivity(_ context.Context, dataDir, host string, port int) error {
	if dataDir == "" {
		return fmt.Errorf("dolt_data_dir is not configured")
	}

	// Kill only the dolt process listening on this specific port.
	portStr := fmt.Sprintf("%d", port)
	_ = exec.Command("pkill", "-f", fmt.Sprintf("dolt sql-server.*--port %s", portStr)).Run()
	time.Sleep(1 * time.Second)

	// Start dolt sql-server in its own session so it survives worker shutdown.
	// Use exec.Command (no context) to avoid SIGKILL on activity cancellation.
	cmd := exec.Command("dolt", "sql-server",
		"--host", host,
		"--port", portStr,
	)
	cmd.Dir = dataDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dolt: %w", err)
	}
	// Release — the process runs independently of this activity.
	go func() { _ = cmd.Wait() }()

	// Wait for server to come up
	time.Sleep(3 * time.Second)

	// Verify
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("dolt not reachable after restart: %w", err)
	}
	conn.Close()
	return nil
}

// AlertDoltFailureActivity sends a failure alert via Matrix.
func (a *DoltHealthActivities) AlertDoltFailureActivity(ctx context.Context, roomID, message string) error {
	if a.ChatSend == nil {
		return nil
	}
	return a.ChatSend.Send(ctx, roomID, message)
}
