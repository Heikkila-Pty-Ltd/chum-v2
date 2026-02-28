package watch

import (
	"net"
	"strconv"
	"testing"
)

func TestCheckDoltHealthActivity_IPv6Address(t *testing.T) {
	// Verify IPv6 addresses don't panic or produce malformed addresses.
	// We can't connect but the function should return cleanly.
	healthy, err := CheckDoltHealthActivity(t.Context(), "::1", 19999)
	if err != nil {
		t.Fatalf("unexpected error with IPv6 address: %v", err)
	}
	if healthy {
		t.Fatal("expected unhealthy for unreachable IPv6 port")
	}
}

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
