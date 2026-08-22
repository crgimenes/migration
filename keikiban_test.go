package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempKeikibanConfig(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "init.filo")
	if content != "" {
		err := os.WriteFile(path, []byte(content), 0o600)
		if err != nil {
			t.Fatalf("write keikiban config: %v", err)
		}
	}
	orig := keikibanConfigPath
	keikibanConfigPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { keikibanConfigPath = orig })
}

func TestKeikibanDatabases(t *testing.T) {
	tempKeikibanConfig(t, `; keikiban configuration
(database "postgres://u:secret@db1:5432/appdb" "production")
(database "host=db2 dbname=other password=secret")
(database "postgres://db3/analytics")
`)

	conns, err := keikibanDatabases()
	if err != nil {
		t.Fatalf("keikibanDatabases: %v", err)
	}
	if len(conns) != 3 {
		t.Fatalf("expected 3 databases, got %v", conns)
	}
	if conns[0].Title != "production" {
		t.Errorf("title = %q, want production", conns[0].Title)
	}
	if strings.Contains(conns[0].MaskedURL, "secret") || strings.Contains(conns[1].MaskedURL, "secret") {
		t.Errorf("password leaked in masked URLs: %+v", conns[:2])
	}
	if conns[2].Title != "analytics" {
		t.Errorf("derived title = %q, want analytics", conns[2].Title)
	}
}

func TestKeikibanDatabasesMissingFile(t *testing.T) {
	tempKeikibanConfig(t, "")
	conns, err := keikibanDatabases()
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if conns != nil {
		t.Errorf("expected no suggestions, got %v", conns)
	}
}

func TestSuggestionsFilterAndURL(t *testing.T) {
	tempConfig(t)
	tempKeikibanConfig(t, `(database "postgres://u:secret@db1/appdb")
(database "host=db2 dbname=kv password=x")
(database "postgres://db3/analytics")
`)

	// db1 is already saved in migration: it must not be suggested.
	err := appendConnection("postgres://u:secret@db1/appdb", "/tmp/m", "")
	if err != nil {
		t.Fatalf("appendConnection: %v", err)
	}

	svc := &guiService{}
	conns, err := svc.Suggestions()
	if err != nil {
		t.Fatalf("Suggestions: %v", err)
	}
	// The key=value form is unusable by migration and filtered out too.
	if len(conns) != 1 || conns[0].Title != "analytics" {
		t.Fatalf("Suggestions = %+v, want only analytics", conns)
	}

	real, err := svc.SuggestionURL(0)
	if err != nil {
		t.Fatalf("SuggestionURL: %v", err)
	}
	if real != "postgres://db3/analytics" {
		t.Errorf("SuggestionURL = %q", real)
	}

	_, err = svc.SuggestionURL(5)
	if err == nil {
		t.Error("expected error for out-of-range suggestion")
	}
}

func TestResolveGUITarget(t *testing.T) {
	tempConfig(t)
	err := appendConnection("postgres://a/first", "/dir/first", "")
	if err != nil {
		t.Fatalf("appendConnection: %v", err)
	}
	err = appendConnection("postgres://b/second", "/dir/second", "")
	if err != nil {
		t.Fatalf("appendConnection: %v", err)
	}

	tests := []struct {
		name    string
		url     string
		dir     string
		wantURL string
		wantDir string
	}{
		{"complete target untouched", "postgres://x/y", "/d", "postgres://x/y", "/d"},
		{"url finds its saved dir", "postgres://b/second", "", "postgres://b/second", "/dir/second"},
		{"unknown url keeps empty dir", "postgres://c/none", "", "postgres://c/none", ""},
		{"empty falls back to first saved", "", "", "postgres://a/first", "/dir/first"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotDir := resolveGUITarget(tt.url, tt.dir)
			if gotURL != tt.wantURL || gotDir != tt.wantDir {
				t.Errorf("resolveGUITarget(%q, %q) = (%q, %q), want (%q, %q)",
					tt.url, tt.dir, gotURL, gotDir, tt.wantURL, tt.wantDir)
			}
		})
	}
}

func TestAppliedVersion(t *testing.T) {
	ctx := context.Background()
	migrations := t.TempDir()
	writeSQLiteMigrations(t, migrations)
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "applied.db")

	tempConfig(t)
	err := runMigration(ctx, migrations, dbURL, "up", false, false)
	if err != nil {
		t.Fatalf("up failed: %v", err)
	}

	v := appliedVersion(ctx, dbURL)
	if v == nil || *v != 2 {
		t.Errorf("appliedVersion = %v, want 2", v)
	}

	if appliedVersion(ctx, "mysql://bad") != nil {
		t.Error("appliedVersion on a bad URL must be nil")
	}
}
