package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crgimenes/migration/introspect"
	"github.com/crgimenes/migration/snapshot"
)

func writeSQLiteMigrations(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"001_create.up.sql":   "CREATE TABLE IF NOT EXISTS test (id INTEGER PRIMARY KEY);",
		"001_create.down.sql": "DROP TABLE IF EXISTS test;",
		"002_alter.up.sql":    "ALTER TABLE test ADD COLUMN name TEXT;",
		"002_alter.down.sql":  "ALTER TABLE test DROP COLUMN name;",
	}
	for name, content := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
		if err != nil {
			t.Fatalf("failed to create test file %s: %v", name, err)
		}
	}
}

func TestFormatFileSize(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, size int) string {
		path := filepath.Join(dir, name)
		err := os.WriteFile(path, make([]byte, size), 0o644)
		if err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
		return path
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{"missing file", filepath.Join(dir, "missing"), ""},
		{"bytes", write("small", 100), "(100 bytes)"},
		{"kilobytes", write("kb", 2048), "(2.0 KB)"},
		{"megabytes", write("mb", 2*1024*1024), "(2.0 MB)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatFileSize(tt.path)
			if got != tt.want {
				t.Errorf("formatFileSize(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestColorize(t *testing.T) {
	t.Setenv("TERM", "dumb")
	got := colorize("text", ColorRed)
	if got != "text" {
		t.Errorf("colorize with TERM=dumb = %q, want plain text", got)
	}

	t.Setenv("TERM", "xterm-256color")
	got = colorize("text", ColorRed)
	want := ColorRed + "text" + ColorReset
	if got != want {
		t.Errorf("colorize with TERM=xterm = %q, want %q", got, want)
	}
}

func Test_runMigration(t *testing.T) {
	ctx := context.Background()
	migrations := t.TempDir()
	writeSQLiteMigrations(t, migrations)
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "cli.db")

	err := runMigration(ctx, migrations, dbURL, "up", false, false)
	if err != nil {
		t.Fatalf("runMigration up failed: %v", err)
	}

	err = runMigration(ctx, migrations, dbURL, "status", true, false)
	if err != nil {
		t.Fatalf("runMigration status -json failed: %v", err)
	}

	err = runMigration(ctx, migrations, dbURL, "down", false, false)
	if err != nil {
		t.Fatalf("runMigration down failed: %v", err)
	}

	err = runMigration(ctx, migrations, dbURL, "bogus", false, false)
	if err == nil {
		t.Error("expected error for unknown action")
	}

	err = runMigration(ctx, migrations, dbURL, "bogus", true, false)
	if err == nil {
		t.Error("expected error for unknown action in JSON mode")
	}
}

func TestReportBetweenSnapshots(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	older := &introspect.Schema{
		Format: introspect.FormatVersion,
		Tables: []introspect.Table{
			{Schema: "public", Name: "users", Columns: []introspect.Column{
				{Name: "id", Type: "integer", NotNull: true},
			}},
		},
	}
	newer := &introspect.Schema{
		Format: introspect.FormatVersion,
		Tables: []introspect.Table{
			{Schema: "public", Name: "users", Columns: []introspect.Column{
				{Name: "id", Type: "integer", NotNull: true},
				{Name: "email", Type: "text"},
			}},
		},
	}

	_, err := snapshot.Write(dir, 1, older)
	if err != nil {
		t.Fatalf("failed to write snapshot 1: %v", err)
	}
	_, err = snapshot.Write(dir, 2, newer)
	if err != nil {
		t.Fatalf("failed to write snapshot 2: %v", err)
	}

	// No database URL: report between two snapshots works offline.
	err = cmdReport(ctx, "", dir, []string{"1", "2"}, false)
	if err != nil {
		t.Fatalf("report failed: %v", err)
	}
	err = cmdReport(ctx, "", dir, []string{"2", "1"}, true)
	if err != nil {
		t.Fatalf("report -json failed: %v", err)
	}

	err = cmdReport(ctx, "", dir, []string{"1"}, false)
	if err == nil {
		t.Error("expected usage error with a single argument")
	}

	err = cmdReport(ctx, "", dir, []string{"1", "live"}, false)
	if err == nil {
		t.Error("expected error for `live` without a database URL")
	}
}

func TestExecute(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MIGRATIONS", "")
	t.Setenv("ACTION", "")

	origArgs := os.Args
	defer func() {
		os.Args = origArgs
	}()

	migrations := t.TempDir()
	writeSQLiteMigrations(t, migrations)
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "exec.db")

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"version flag", []string{"migration", "-version"}, ""},
		{"help flag", []string{"migration", "-help"}, ""},
		{"missing action", []string{"migration"}, "action is required"},
		{"missing dir", []string{"migration", "-url", dbURL, "up"}, "migrations directory is required"},
		{"missing url", []string{"migration", "-dir", migrations, "up"}, "database URL is required"},
		{"legacy action flag", []string{"migration", "-url", dbURL, "-dir", migrations, "-action", "up"}, ""},
		{"positional action", []string{"migration", "-url", dbURL, "-dir", migrations, "status"}, ""},
		{"positional with count", []string{"migration", "-url", dbURL, "-dir", migrations, "down", "1"}, ""},
		{"json output", []string{"migration", "-json", "-url", dbURL, "-dir", migrations, "status"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Execute registers its flags on the global CommandLine;
			// reset it so each case starts clean.
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
			os.Args = tt.args

			err := Execute()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Execute() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Execute() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Execute() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
