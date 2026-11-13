package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFileVersion(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{"testdata/001_name.up.sql", 1},
		{"dir/010_add_index.down.sql", 10},
		{"42_only.up.sql", 42},
		{"no_number.sql", 0},
		{"plain.sql", 0},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := FileVersion(tt.path)
			if got != tt.want {
				t.Errorf("FileVersion(%q) = %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}

func Test_parsePar(t *testing.T) {
	tests := []struct {
		name    string
		m       []string
		want    int
		wantErr bool
	}{
		{"no number", []string{"up"}, 0, false},
		{"with number", []string{"up", "3"}, 3, false},
		{"invalid number", []string{"up", "abc"}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePar(tt.m)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePar(%v) error = %v, wantErr %v", tt.m, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parsePar(%v) = %d, want %d", tt.m, got, tt.want)
			}
		})
	}
}

func TestSQLiteDataSource(t *testing.T) {
	// A SQLite URL carries a filesystem path, so the conversion must
	// survive shapes url.Parse cannot handle: Windows drive letters
	// (which parse as an "invalid port") and relative paths (whose
	// first segment url.Parse would read as a host and drop).
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"memory", "sqlite::memory:", ":memory:"},
		{"absolute unix", "sqlite:///tmp/app.db", "/tmp/app.db"},
		{"windows backslash", `sqlite://C:\data\app.db`, `C:\data\app.db`},
		{"windows forward slash", "sqlite://C:/data/app.db", "C:/data/app.db"},
		{"relative keeps first segment", "sqlite://data/app.db", "data/app.db"},
		{"dot relative", "sqlite://./app.db", "./app.db"},
		{"no slashes", "sqlite:app.db", "app.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SQLiteDataSource(tt.in)
			if got != tt.want {
				t.Errorf("SQLiteDataSource(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGetDatabaseConfigWindowsPath(t *testing.T) {
	// Regression: url.Parse rejected this outright, so SQLite file
	// databases were unusable on Windows.
	config, err := GetDatabaseConfig(`sqlite://C:\data\app.db`)
	if err != nil {
		t.Fatalf("GetDatabaseConfig with a Windows path failed: %v", err)
	}
	if config.Type != SQLite {
		t.Errorf("type = %v, want SQLite", config.Type)
	}
}

func TestRunRefusesUnreadableDir(t *testing.T) {
	// A missing directory must NOT read as "no migrations": Glob hides
	// it behind an empty result, so `status` would report everything up
	// to date and `up` would claim success having applied nothing.
	ctx := context.Background()
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "guard.db")
	missing := filepath.Join(t.TempDir(), "not-here")

	for _, action := range []string{"status", "up", "down"} {
		t.Run(action, func(t *testing.T) {
			_, _, err := Run(ctx, missing, dbURL, action)
			if err == nil {
				t.Fatalf("Run(%q) on a missing directory returned no error", action)
			}
			if !strings.Contains(err.Error(), "cannot read migrations directory") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}

	// An empty directory is legitimate: a project with no migrations.
	empty := t.TempDir()
	n, _, err := Run(ctx, empty, dbURL, "status")
	if err != nil {
		t.Fatalf("empty directory must be accepted: %v", err)
	}
	if n != 0 {
		t.Errorf("status on an empty directory = %d, want 0", n)
	}
}

func TestRunUnsupportedScheme(t *testing.T) {
	_, _, err := Run(context.Background(), "testdata", "mysql://user:pass@localhost/db", "up")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestRunSQLiteFile(t *testing.T) {
	ctx := context.Background()
	migrations := t.TempDir()
	createSQLiteTestFiles(t, migrations)

	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "test.db")

	n, _, err := Run(ctx, migrations, dbURL, "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 pending migrations, got %d", n)
	}

	n, executed, err := Run(ctx, migrations, dbURL, "up")
	if err != nil {
		t.Fatalf("up failed: %v", err)
	}
	if n != 3 || len(executed) != 3 {
		t.Errorf("expected 3 migrations executed, got n=%d executed=%v", n, executed)
	}

	n, _, err = Run(ctx, migrations, dbURL, "down 1")
	if err != nil {
		t.Fatalf("down 1 failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 migration reverted, got %d", n)
	}

	n, _, err = Run(ctx, migrations, dbURL, "down")
	if err != nil {
		t.Fatalf("down failed: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 migrations reverted, got %d", n)
	}

	_, _, err = Run(ctx, migrations, dbURL, "bogus")
	if err == nil {
		t.Error("expected error for unknown action")
	}

	_, _, err = Run(ctx, migrations, dbURL, "  ")
	if err == nil {
		t.Error("expected error for empty action")
	}

	_, _, err = Run(ctx, migrations, dbURL, "up abc")
	if err == nil {
		t.Error("expected error for non-numeric count")
	}
}

func TestOpenDatabaseInvalidPath(t *testing.T) {
	config, err := GetDatabaseConfig("sqlite:///nonexistent-dir-xyz/sub/db.sqlite")
	if err != nil {
		t.Fatalf("failed to get database config: %v", err)
	}
	_, err = OpenDatabase("sqlite:///nonexistent-dir-xyz/sub/db.sqlite", config)
	if err == nil {
		t.Fatal("expected error opening database in nonexistent directory")
	}
}

func writeMigrations(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		writeFile(t, filepath.Join(dir, name+".up.sql"), "SELECT 1;")
		writeFile(t, filepath.Join(dir, name+".down.sql"), "SELECT 1;")
	}
}

func run(t *testing.T, dir, dbURL, action string) (int, []string) {
	t.Helper()
	n, files, err := Run(context.Background(), dir, dbURL, action)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return n, files
}

func baseNames(files []string) []string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	return names
}

func TestUpGappedVersions(t *testing.T) {
	dir := t.TempDir()
	writeMigrations(t, dir, "010_a", "020_b", "030_c")
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "gap.db")

	n, _ := run(t, dir, dbURL, "up 1")
	if n != 1 {
		t.Fatalf("up 1 applied %d, want 1", n)
	}
	n, files := run(t, dir, dbURL, "up")
	want := []string{"020_b.up.sql", "030_c.up.sql"}
	if n != 2 || !slices.Equal(baseNames(files), want) {
		t.Fatalf("up applied %v, want %v", baseNames(files), want)
	}
	n, _ = run(t, dir, dbURL, "status")
	if n != 0 {
		t.Fatalf("status pending %d, want 0", n)
	}
}

func TestUpNumericOrderAndLateFile(t *testing.T) {
	dir := t.TempDir()
	writeMigrations(t, dir, "9_a", "10_b")
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "order.db")

	_, files := run(t, dir, dbURL, "up")
	want := []string{"9_a.up.sql", "10_b.up.sql"}
	if !slices.Equal(baseNames(files), want) {
		t.Fatalf("up order %v, want %v", baseNames(files), want)
	}

	writeMigrations(t, dir, "5_late")
	n, files := run(t, dir, dbURL, "status")
	if n != 1 || filepath.Base(files[0]) != "5_late.up.sql" {
		t.Fatalf("status = %d %v, want the late file pending", n, baseNames(files))
	}
	_, files = run(t, dir, dbURL, "up")
	if !slices.Equal(baseNames(files), []string{"5_late.up.sql"}) {
		t.Fatalf("up applied %v, want the late file", baseNames(files))
	}

	_, files = run(t, dir, dbURL, "down 2")
	want = []string{"10_b.down.sql", "9_a.down.sql"}
	if !slices.Equal(baseNames(files), want) {
		t.Fatalf("down 2 reverted %v, want %v", baseNames(files), want)
	}
}

func TestDownMissingFile(t *testing.T) {
	dir := t.TempDir()
	writeMigrations(t, dir, "001_a", "002_b")
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "down.db")
	run(t, dir, dbURL, "up")

	err := os.Remove(filepath.Join(dir, "002_b.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Run(context.Background(), dir, dbURL, "down 1")
	if err == nil || !strings.Contains(err.Error(), "no down file for applied version 2") {
		t.Fatalf("down error = %v, want missing down file", err)
	}
	n, _ := run(t, dir, dbURL, "status")
	if n != 0 {
		t.Fatalf("status pending %d after failed down, want 0 (rolled back)", n)
	}
}

func TestMigrationFilesRefusesAmbiguousNames(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{"no version", []string{"setup.up.sql"}, "no numeric version prefix"},
		{"duplicate version", []string{"001_a.up.sql", "1_b.up.sql"}, "share version 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				writeFile(t, filepath.Join(dir, f), "SELECT 1;")
			}
			_, err := migrationFiles(dir, ".up.sql")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("migrationFiles error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParseParNegative(t *testing.T) {
	_, err := parsePar([]string{"up", "-1"})
	if err == nil {
		t.Fatal("expected error for a negative count")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
