package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crgimenes/glaze"
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

func TestGUIServiceUnconfigured(t *testing.T) {
	svc := &guiService{}

	info, err := svc.Info()
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if info.Configured {
		t.Error("empty service reported as configured")
	}

	_, err = svc.Status()
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("Status = %v, want not-connected error", err)
	}
	_, err = svc.Drift()
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("Drift = %v, want not-connected error", err)
	}
	_, err = svc.Snapshots()
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("Snapshots = %v, want not-connected error", err)
	}
	_, err = svc.Report("1", "2")
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("Report = %v, want not-connected error", err)
	}
}

func TestGUIServiceConnect(t *testing.T) {
	tempConfig(t)
	migrations := t.TempDir()
	writeSQLiteMigrations(t, migrations)
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "conn.db")
	svc := &guiService{}

	_, err := svc.Connect("", "")
	if err == nil {
		t.Error("expected error for empty target")
	}

	_, err = svc.Connect(dbURL, filepath.Join(migrations, "does-not-exist"))
	if err == nil {
		t.Error("expected error for missing directory")
	}

	_, err = svc.Connect("mysql://nope", migrations)
	if err == nil {
		t.Error("expected error for unsupported scheme")
	}

	info, err := svc.Connect(dbURL, migrations)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !info.Configured {
		t.Error("Connect succeeded but info not configured")
	}

	st, err := svc.Status()
	if err != nil {
		t.Fatalf("Status after Connect failed: %v", err)
	}
	if st.Pending != 2 {
		t.Errorf("Status = %+v, want pending 2", st)
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
	tempConfig(t)
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

func TestGUIServiceRunFlowSQLite(t *testing.T) {
	tempConfig(t)
	migrations := t.TempDir()
	writeSQLiteMigrations(t, migrations)
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "flow.db")
	svc := &guiService{}

	_, err := svc.Connect(dbURL, migrations)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	p, err := svc.UpPreview()
	if err != nil {
		t.Fatalf("UpPreview failed: %v", err)
	}
	if len(p.Files) != 2 || !strings.Contains(p.SQL, "CREATE TABLE") {
		t.Errorf("UpPreview = %+v, want 2 files with CREATE TABLE SQL", p)
	}

	st, err := svc.RunUp()
	if err != nil {
		t.Fatalf("RunUp failed: %v", err)
	}
	if st.Applied != 2 || st.Pending != 0 {
		t.Errorf("after RunUp = %+v, want applied 2 pending 0", st)
	}

	p, err = svc.DownPreview()
	if err != nil {
		t.Fatalf("DownPreview failed: %v", err)
	}
	if len(p.Files) != 1 || !strings.Contains(p.Files[0], "002_") {
		t.Errorf("DownPreview = %+v, want the version-2 down file", p)
	}

	st, err = svc.RunDown()
	if err != nil {
		t.Fatalf("RunDown failed: %v", err)
	}
	if st.Applied != 1 || st.Pending != 1 {
		t.Errorf("after RunDown = %+v, want applied 1 pending 1", st)
	}

	_, err = svc.Capture("x")
	if err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Errorf("Capture on sqlite = %v, want PostgreSQL requirement", err)
	}

	_, err = svc.Capture("Bad Name!")
	if err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Errorf("Capture with invalid name = %v, want name validation error", err)
	}
}

func TestGUIServiceCaptureFlowPostgres(t *testing.T) {
	tempConfig(t)
	ctx := context.Background()
	dbURL := scratchDatabase(t, "migration_gui_capture")
	migrations := t.TempDir()
	writeInitialMigration(t, migrations)

	err := runMigration(ctx, migrations, dbURL, "up", false, false)
	if err != nil {
		t.Fatalf("up failed: %v", err)
	}

	svc := &guiService{}
	_, err = svc.Connect(dbURL, migrations)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	_, err = db.Exec("CREATE INDEX idx_users_name ON users (name)")
	if err != nil {
		t.Fatalf("failed to create drift: %v", err)
	}

	p, err := svc.CapturePreview()
	if err != nil {
		t.Fatalf("CapturePreview failed: %v", err)
	}
	if !p.Drift || !strings.Contains(p.UpSQL, "CREATE INDEX idx_users_name") {
		t.Fatalf("CapturePreview = %+v, want drift with index DDL", p)
	}
	if !strings.Contains(p.DownSQL, "DROP INDEX") {
		t.Errorf("CapturePreview down = %q, want DROP INDEX", p.DownSQL)
	}

	out, err := svc.Capture("gui_capture")
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}
	if !out.Drift || out.Version != 2 {
		t.Errorf("Capture = %+v, want version 2", out)
	}
	if !strings.Contains(out.UpFile, "002_gui_capture.up.sql") {
		t.Errorf("Capture up file = %q", out.UpFile)
	}

	d, err := svc.Drift()
	if err != nil {
		t.Fatalf("Drift after capture failed: %v", err)
	}
	if d.Drift {
		t.Errorf("expected no drift after capture, got %+v", d)
	}
}

func TestGUIServiceSavedConnections(t *testing.T) {
	tempConfig(t)
	migrations := t.TempDir()
	writeSQLiteMigrations(t, migrations)
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "saved.db")
	svc := &guiService{}

	conns, err := svc.Connections()
	if err != nil {
		t.Fatalf("Connections failed: %v", err)
	}
	if len(conns) != 0 {
		t.Errorf("expected empty list, got %v", conns)
	}

	_, err = svc.Connect(dbURL, migrations)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	conns, err = svc.Connections()
	if err != nil {
		t.Fatalf("Connections after Connect failed: %v", err)
	}
	if len(conns) != 1 || conns[0].Dir != migrations {
		t.Fatalf("Connections = %+v, want the connected target saved", conns)
	}

	fresh := &guiService{}
	info, err := fresh.ConnectSaved(0)
	if err != nil {
		t.Fatalf("ConnectSaved failed: %v", err)
	}
	if !info.Configured {
		t.Error("ConnectSaved succeeded but info not configured")
	}

	_, err = fresh.ConnectSaved(9)
	if err == nil {
		t.Error("expected error for out-of-range saved connection")
	}

	remaining, err := svc.RemoveConnection(0)
	if err != nil {
		t.Fatalf("RemoveConnection failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected empty list after removal, got %v", remaining)
	}
}

func TestGUIServicePickDirectoryHeadless(t *testing.T) {
	svc := &guiService{}
	_, err := svc.PickDirectory("")
	if err == nil || !strings.Contains(err.Error(), "no window") {
		t.Errorf("PickDirectory without a window = %v, want no-window error", err)
	}
}

func TestServeAsset(t *testing.T) {
	// The UI is served through the app:// scheme, not a loopback HTTP
	// server: the sandbox forbids binding a listening socket.
	tests := []struct {
		name     string
		url      string
		wantMIME string
	}{
		{"root serves index", "app://migration/", "text/html"},
		{"explicit index", "app://migration/index.html", "text/html"},
		{"stylesheet", "app://migration/style.css", "text/css"},
		{"script", "app://migration/app.js", "text/javascript"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := serveAsset(&glaze.SchemeRequest{URL: tt.url})
			if resp == nil {
				t.Fatalf("serveAsset(%q) = nil, want a response", tt.url)
			}
			if len(resp.Body) == 0 {
				t.Errorf("serveAsset(%q) returned an empty body", tt.url)
			}
			if !strings.Contains(resp.MIMEType, tt.wantMIME) {
				t.Errorf("MIME = %q, want it to contain %q", resp.MIMEType, tt.wantMIME)
			}
		})
	}

	if serveAsset(&glaze.SchemeRequest{URL: "app://migration/nope.txt"}) != nil {
		t.Error("a missing asset must answer nil (not found)")
	}
	// Path traversal must not escape the embedded FS.
	if serveAsset(&glaze.SchemeRequest{URL: "app://migration/../../gui.go"}) != nil {
		t.Error("traversal outside the embedded ui/ must not resolve")
	}
}
