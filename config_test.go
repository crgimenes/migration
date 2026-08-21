package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "init.filo")
	orig := configPath
	configPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { configPath = orig })
	return path
}

func TestConnectionsRoundTrip(t *testing.T) {
	path := tempConfig(t)

	conns, err := loadConnections()
	if err != nil {
		t.Fatalf("loadConnections on missing file: %v", err)
	}
	if conns != nil {
		t.Errorf("expected nil for missing file, got %v", conns)
	}

	err = appendConnection("postgres://u:secret@db1/app", "./migrations", "")
	if err != nil {
		t.Fatalf("appendConnection failed: %v", err)
	}
	err = appendConnection("sqlite:///tmp/x.db", "/tmp/m", "local dev")
	if err != nil {
		t.Fatalf("appendConnection failed: %v", err)
	}
	// Duplicate url+dir must be left alone (append-only, no rewrite).
	err = appendConnection("postgres://u:secret@db1/app", "./migrations", "")
	if err != nil {
		t.Fatalf("appendConnection duplicate failed: %v", err)
	}

	conns, err = loadConnections()
	if err != nil {
		t.Fatalf("loadConnections failed: %v", err)
	}
	if len(conns) != 2 {
		t.Fatalf("expected 2 connections, got %v", conns)
	}
	if conns[0].Title != "app" {
		t.Errorf("derived title = %q, want app (database name)", conns[0].Title)
	}
	if !strings.Contains(conns[0].MaskedURL, "u:***@") {
		t.Errorf("masked url = %q, want password hidden", conns[0].MaskedURL)
	}
	if conns[1].Title != "local dev" || conns[1].Dir != "/tmp/m" {
		t.Errorf("second connection = %+v", conns[1])
	}

	// The file belongs to the user: a comment must survive removal of a
	// neighboring line.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("failed to open config: %v", err)
	}
	_, err = f.WriteString("; my note\n")
	if err != nil {
		t.Fatalf("failed to append comment: %v", err)
	}
	_ = f.Close()

	err = removeConnection(0)
	if err != nil {
		t.Fatalf("removeConnection failed: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if !strings.Contains(string(content), "; my note") {
		t.Errorf("user comment lost on removal:\n%s", content)
	}
	if strings.Contains(string(content), "db1") {
		t.Errorf("removed connection still present:\n%s", content)
	}

	conns, err = loadConnections()
	if err != nil {
		t.Fatalf("loadConnections after removal failed: %v", err)
	}
	if len(conns) != 1 || conns[0].Dir != "/tmp/m" {
		t.Errorf("after removal = %+v, want only the sqlite entry", conns)
	}
}

func TestRemoveConnectionOutOfRange(t *testing.T) {
	tempConfig(t)
	err := appendConnection("sqlite::memory:", "/tmp/m", "")
	if err != nil {
		t.Fatalf("appendConnection failed: %v", err)
	}
	err = removeConnection(5)
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

func TestParseConnectionsCommentOnly(t *testing.T) {
	conns, err := parseConnections("; just a comment\n\n")
	if err != nil {
		t.Fatalf("comment-only config must parse: %v", err)
	}
	if conns != nil {
		t.Errorf("expected no connections, got %v", conns)
	}
}

func TestParseConnectionsQuoting(t *testing.T) {
	src := `(connection "postgres://u@h/db" "/path/with \"quotes\"" "ti\ttle")`
	conns, err := parseConnections(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(conns) != 1 || conns[0].Dir != `/path/with "quotes"` {
		t.Errorf("quoted dir = %+v", conns)
	}
}

func TestFiloQuoteRoundTrip(t *testing.T) {
	tempConfig(t)
	dir := "/weird \"dir\"\twith\nstuff"
	err := appendConnection("sqlite::memory:", dir, "")
	if err != nil {
		t.Fatalf("appendConnection failed: %v", err)
	}
	conns, err := loadConnections()
	if err != nil {
		t.Fatalf("loadConnections failed: %v", err)
	}
	if len(conns) != 1 || conns[0].Dir != dir {
		t.Errorf("round trip changed the dir: %q -> %q", dir, conns[0].Dir)
	}
}

func TestDefaultTitle(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"postgres://u:p@host:5432/appdb", "appdb"},
		{"postgres://u@host:5432", "host:5432"},
		{"sqlite:///var/data/site.db", "site.db"},
		{"sqlite::memory:", "connection"},
		{`sqlite://C:\data\site.db`, "site.db"},
	}
	for _, tt := range tests {
		got := defaultTitle(tt.url)
		if got != tt.want {
			t.Errorf("defaultTitle(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
