package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestRedactURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"postgres://user:secret@localhost:5432/db", "postgres://user:***@localhost:5432/db"},
		{"postgres://user@localhost:5432/db", "postgres://user@localhost:5432/db"},
		{"postgres://localhost:5432/db", "postgres://localhost:5432/db"},
		{"sqlite::memory:", "sqlite::memory:"},
	}
	for _, tt := range tests {
		got := redactURL(tt.in)
		if got != tt.want {
			t.Errorf("redactURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGUIServiceStatusSQLite(t *testing.T) {
	migrations := t.TempDir()
	writeSQLiteMigrations(t, migrations)
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "gui.db")
	svc := &guiService{dbURL: dbURL, dir: migrations}

	info, err := svc.Info()
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if info.Postgres {
		t.Error("sqlite URL reported as postgres")
	}

	st, err := svc.Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if st.Applied != 0 || st.Pending != 2 {
		t.Errorf("Status = %+v, want applied 0 pending 2", st)
	}

	err = runMigration(context.Background(), migrations, dbURL, "up", false, false)
	if err != nil {
		t.Fatalf("up failed: %v", err)
	}

	st, err = svc.Status()
	if err != nil {
		t.Fatalf("Status after up failed: %v", err)
	}
	if st.Applied != 2 || st.Pending != 0 {
		t.Errorf("Status after up = %+v, want applied 2 pending 0", st)
	}
}

func TestGUIServiceDriftRequiresPostgres(t *testing.T) {
	svc := &guiService{dbURL: "sqlite::memory:", dir: t.TempDir()}
	_, err := svc.Drift()
	if err == nil {
		t.Fatal("expected error for sqlite drift")
	}
	if !strings.Contains(err.Error(), "PostgreSQL") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGUIServiceDriftAndReport(t *testing.T) {
	ctx := context.Background()
	dbURL := scratchDatabase(t, "migration_gui_svc")
	migrations := t.TempDir()
	writeInitialMigration(t, migrations)

	err := runMigration(ctx, migrations, dbURL, "up", false, false)
	if err != nil {
		t.Fatalf("up failed: %v", err)
	}

	svc := &guiService{dbURL: dbURL, dir: migrations}

	d, err := svc.Drift()
	if err != nil {
		t.Fatalf("Drift failed: %v", err)
	}
	if d.Drift {
		t.Errorf("expected no drift right after up, got %+v", d)
	}

	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	_, err = db.Exec("ALTER TABLE users ADD COLUMN email text")
	if err != nil {
		t.Fatalf("failed to create drift: %v", err)
	}

	d, err = svc.Drift()
	if err != nil {
		t.Fatalf("Drift after change failed: %v", err)
	}
	if !d.Drift || len(d.Changes) != 1 {
		t.Fatalf("Drift = %+v, want one change", d)
	}
	if !strings.Contains(d.Command, "capture") {
		t.Errorf("Drift.Command = %q, want capture suggestion", d.Command)
	}
	if d.Summary != "1 column added" {
		t.Errorf("Drift.Summary = %q, want '1 column added'", d.Summary)
	}

	versions, err := svc.Snapshots()
	if err != nil {
		t.Fatalf("Snapshots failed: %v", err)
	}
	if len(versions) != 1 || versions[0] != 1 {
		t.Errorf("Snapshots = %v, want [1]", versions)
	}

	r, err := svc.Report("1", "live")
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}
	if !r.Drift || len(r.Changes) != 1 {
		t.Errorf("Report = %+v, want one change", r)
	}
}

func TestStartUIServer(t *testing.T) {
	baseURL, err := startUIServer()
	if err != nil {
		t.Fatalf("startUIServer failed: %v", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}

	for _, path := range []string{"/style.css", "/app.js"} {
		r, getErr := client.Get(baseURL + path)
		if getErr != nil {
			t.Fatalf("GET %s failed: %v", path, getErr)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, r.StatusCode)
		}
	}
}
