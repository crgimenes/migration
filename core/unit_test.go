package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test_version(t *testing.T) {
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
			got := version(tt.path)
			if got != tt.want {
				t.Errorf("version(%q) = %d, want %d", tt.path, got, tt.want)
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

func Test_downFiles_moreAppliedThanFiles(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "001_a.down.sql"), []byte("SELECT 1;"), 0o644)
	if err != nil {
		t.Fatalf("failed to write down file: %v", err)
	}

	_, err = downFiles(dir, 2)
	if err == nil {
		t.Fatal("expected error when applied count exceeds down files on disk")
	}
	if !strings.Contains(err.Error(), "2 migrations applied but only 1 down files") {
		t.Errorf("unexpected error message: %v", err)
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

func TestGetMigrationMaxTxEmpty(t *testing.T) {
	ctx := context.Background()
	dbURL := "sqlite::memory:"

	config, err := GetDatabaseConfig(dbURL)
	if err != nil {
		t.Fatalf("failed to get database config: %v", err)
	}
	db, err := OpenDatabase(dbURL, config)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer func() {
		closeErr := db.Close()
		if closeErr != nil {
			t.Logf("Warning: failed to close database: %v", closeErr)
		}
	}()

	err = CheckAndCreateMigrationsTable(ctx, db, config)
	if err != nil {
		t.Fatalf("failed to create migrations table: %v", err)
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	maxVersion, err := GetMigrationMaxTx(ctx, tx)
	if err != nil {
		t.Fatalf("GetMigrationMaxTx failed: %v", err)
	}
	if maxVersion != 0 {
		t.Errorf("expected max version 0 on empty table, got %d", maxVersion)
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
