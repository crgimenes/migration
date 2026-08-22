package drift

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The full capture round trip runs against a live PostgreSQL in the
// main package's tests; here live the guards that need no database.

func TestLiveDiffNoSnapshot(t *testing.T) {
	_, _, _, _, err := LiveDiff(context.Background(), nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error when no snapshot exists")
	}
	if !strings.Contains(err.Error(), "no snapshot found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWriteMigrationPairRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	up := filepath.Join(dir, "002_x.up.sql")
	down := filepath.Join(dir, "002_x.down.sql")

	err := os.WriteFile(up, []byte("-- existing"), 0o644)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	err = writeMigrationPair(up, down, "a", "b")
	if err == nil {
		t.Fatal("expected refusal to overwrite an existing migration file")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(up)
	if err != nil || string(content) != "-- existing" {
		t.Errorf("existing file was touched: %q, %v", content, err)
	}
}
