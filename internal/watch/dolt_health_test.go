package watch

import (
	"net"
	"strconv"
	"testing"
)

func TestCheckDoltHealthActivity_Unreachable(t *testing.T) {
	// Use a port that's almost certainly not listening
	healthy, err := CheckDoltHealthActivity(t.Context(), "127.0.0.1", 19999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if healthy {
		t.Fatal("expected unhealthy for unreachable port")
	}
}

func TestCheckDoltHealthActivity_Reachable(t *testing.T) {
	// Start a temporary TCP listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	healthy, err := CheckDoltHealthActivity(t.Context(), "127.0.0.1", port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !healthy {
		t.Fatal("expected healthy for listening port")
	}
}
