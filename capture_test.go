package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/crgimenes/migration/introspect"
	"github.com/crgimenes/migration/snapshot"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// scratchDatabase creates a dedicated database so capture tests never
// race with the introspect package tests running against DATABASE_URL.
func scratchDatabase(t *testing.T, name string) string {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL environment variable not set, skipping capture integration test")
	}

	admin, err := sqlx.Connect("postgres", base)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() {
		_ = admin.Close()
	}()

	_, err = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	if err != nil {
		t.Fatalf("failed to drop scratch database: %v", err)
	}
	_, err = admin.Exec("CREATE DATABASE " + name)
	if err != nil {
		t.Fatalf("failed to create scratch database: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("failed to parse DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func writeInitialMigration(t *testing.T, dir string) {
	t.Helper()
	up := "CREATE TABLE users (id serial PRIMARY KEY, name text NOT NULL);\n"
	down := "DROP TABLE users;\n"
	err := os.WriteFile(filepath.Join(dir, "001_init.up.sql"), []byte(up), 0o644)
	if err != nil {
		t.Fatalf("failed to write up migration: %v", err)
	}
	err = os.WriteFile(filepath.Join(dir, "001_init.down.sql"), []byte(down), 0o644)
	if err != nil {
		t.Fatalf("failed to write down migration: %v", err)
	}
}

func TestCaptureRoundTrip(t *testing.T) {
	ctx := context.Background()
	dbURL := scratchDatabase(t, "migration_capture_rt")
	dir := t.TempDir()
	writeInitialMigration(t, dir)

	err := runMigration(ctx, dir, dbURL, "up", false, false)
	if err != nil {
		t.Fatalf("up failed: %v", err)
	}
	baseline, err := snapshot.Load(dir, 1)
	if err != nil {
		t.Fatalf("auto-snapshot missing after up: %v", err)
	}

	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	// Manual drift whose inverse is non-destructive, so the generated
	// down file is executable end to end.
	_, err = db.Exec(`CREATE INDEX idx_users_name ON users (name);
		ALTER TABLE users ADD CONSTRAINT name_len CHECK (length(name) > 1);`)
	if err != nil {
		t.Fatalf("failed to create drift: %v", err)
	}

	err = cmdDiff(ctx, dbURL, dir, false)
	if !errors.Is(err, errDrift) {
		t.Fatalf("cmdDiff = %v, want errDrift", err)
	}

	err = cmdCapture(ctx, dbURL, dir, "manual_tuning", false)
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	upSQL, err := os.ReadFile(filepath.Join(dir, "002_manual_tuning.up.sql"))
	if err != nil {
		t.Fatalf("captured up file missing: %v", err)
	}
	downSQL, err := os.ReadFile(filepath.Join(dir, "002_manual_tuning.down.sql"))
	if err != nil {
		t.Fatalf("captured down file missing: %v", err)
	}
	captured, err := snapshot.Load(dir, 2)
	if err != nil {
		t.Fatalf("capture snapshot missing: %v", err)
	}

	// After capture there must be no drift left.
	err = cmdDiff(ctx, dbURL, dir, false)
	if err != nil {
		t.Fatalf("cmdDiff after capture = %v, want clean", err)
	}

	// Round trip: down restores the baseline schema...
	_, err = db.Exec(string(downSQL))
	if err != nil {
		t.Fatalf("generated down failed to execute: %v\n%s", err, downSQL)
	}
	now, err := introspect.Read(ctx, db)
	if err != nil {
		t.Fatalf("introspect after down failed: %v", err)
	}
	if !reflect.DeepEqual(now, baseline) {
		t.Errorf("schema after generated down differs from baseline")
	}

	// ...and up restores the captured schema.
	_, err = db.Exec(string(upSQL))
	if err != nil {
		t.Fatalf("generated up failed to execute: %v\n%s", err, upSQL)
	}
	now, err = introspect.Read(ctx, db)
	if err != nil {
		t.Fatalf("introspect after up failed: %v", err)
	}
	if !reflect.DeepEqual(now, captured) {
		t.Errorf("schema after generated up differs from captured state")
	}
}

func TestCaptureDestructiveDownIsCommented(t *testing.T) {
	ctx := context.Background()
	dbURL := scratchDatabase(t, "migration_capture_destr")
	dir := t.TempDir()
	writeInitialMigration(t, dir)

	err := runMigration(ctx, dir, dbURL, "up", false, false)
	if err != nil {
		t.Fatalf("up failed: %v", err)
	}

	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	_, err = db.Exec(`ALTER TABLE users ADD COLUMN email text;`)
	if err != nil {
		t.Fatalf("failed to create drift: %v", err)
	}

	err = cmdCapture(ctx, dbURL, dir, "add_email", false)
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	upSQL, err := os.ReadFile(filepath.Join(dir, "002_add_email.up.sql"))
	if err != nil {
		t.Fatalf("captured up file missing: %v", err)
	}
	if !strings.Contains(string(upSQL), "ALTER TABLE public.users ADD COLUMN email text;") {
		t.Errorf("up missing add column:\n%s", upSQL)
	}

	downSQL, err := os.ReadFile(filepath.Join(dir, "002_add_email.down.sql"))
	if err != nil {
		t.Fatalf("captured down file missing: %v", err)
	}
	if !strings.Contains(string(downSQL), "-- ALTER TABLE public.users DROP COLUMN email;") {
		t.Errorf("destructive down must be commented out:\n%s", downSQL)
	}
	if !strings.Contains(string(downSQL), "-- DESTRUCTIVE:") {
		t.Errorf("destructive down missing warning:\n%s", downSQL)
	}
}

func TestCaptureNoSnapshotFails(t *testing.T) {
	ctx := context.Background()
	dbURL := scratchDatabase(t, "migration_capture_nosnap")
	dir := t.TempDir()

	err := cmdCapture(ctx, dbURL, dir, "x", false)
	if err == nil {
		t.Fatal("expected error when no snapshot exists")
	}
	if !strings.Contains(err.Error(), "no snapshot found") {
		t.Errorf("unexpected error: %v", err)
	}
}
