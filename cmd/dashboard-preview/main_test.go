package main

import "testing"

func TestParseFlags_Defaults(t *testing.T) {
	f := parseFlags([]string{"dashboard-preview"})
	if f.dbPath != "chum.db" {
		t.Errorf("dbPath = %q, want chum.db", f.dbPath)
	}
	if f.tracesDBPath != "chum-traces.db" {
		t.Errorf("tracesDBPath = %q, want chum-traces.db", f.tracesDBPath)
	}
	if f.port != "9780" {
		t.Errorf("port = %q, want 9780", f.port)
	}
}

func TestParseFlags_TracesDB(t *testing.T) {
	f := parseFlags([]string{"dashboard-preview", "--traces-db", "/tmp/traces.db"})
	if f.tracesDBPath != "/tmp/traces.db" {
		t.Errorf("tracesDBPath = %q, want /tmp/traces.db", f.tracesDBPath)
	}
}

func TestParseFlags_AllFlags(t *testing.T) {
	f := parseFlags([]string{"dashboard-preview", "--db", "a.db", "--traces-db", "b.db", "--port", "1234", "--web", "/www", "--config", "c.toml"})
	if f.dbPath != "a.db" {
		t.Errorf("dbPath = %q, want a.db", f.dbPath)
	}
	if f.tracesDBPath != "b.db" {
		t.Errorf("tracesDBPath = %q, want b.db", f.tracesDBPath)
	}
	if f.port != "1234" {
		t.Errorf("port = %q, want 1234", f.port)
	}
	if f.webDir != "/www" {
		t.Errorf("webDir = %q, want /www", f.webDir)
	}
	if f.configPath != "c.toml" {
		t.Errorf("configPath = %q, want c.toml", f.configPath)
	}
}

func TestListenAddress_LocalhostOnly(t *testing.T) {
	if got := listenAddress("1234"); got != "127.0.0.1:1234" {
		t.Fatalf("listenAddress = %q, want %q", got, "127.0.0.1:1234")
	}
}
