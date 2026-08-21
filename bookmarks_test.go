package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirBookmarkRoundTrip(t *testing.T) {
	tempConfig(t)
	dir := t.TempDir()

	// Nothing stored yet: not an error, a no-op release.
	release, err := restoreDirBookmark(dir)
	if err != nil {
		t.Fatalf("restore without a token: %v", err)
	}
	release()

	err = saveDirBookmark(dir)
	if err != nil {
		t.Fatalf("saveDirBookmark: %v", err)
	}

	path, err := bookmarkFile(dir)
	if err != nil {
		t.Fatalf("bookmarkFile: %v", err)
	}
	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("bookmark file missing after save: %v", err)
	}

	release, err = restoreDirBookmark(dir)
	if err != nil {
		t.Fatalf("restoreDirBookmark: %v", err)
	}
	release()
	release()
}

func TestSaveDirBookmarkMissingDir(t *testing.T) {
	tempConfig(t)
	err := saveDirBookmark(filepath.Join(t.TempDir(), "gone"))
	if err == nil {
		t.Fatal("expected error bookmarking a missing directory")
	}
}

func TestConnectStoresBookmark(t *testing.T) {
	tempConfig(t)
	migrations := t.TempDir()
	writeSQLiteMigrations(t, migrations)
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "bm.db")

	svc := &guiService{}
	_, err := svc.Connect(dbURL, migrations)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	path, err := bookmarkFile(migrations)
	if err != nil {
		t.Fatalf("bookmarkFile: %v", err)
	}
	token, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Connect did not store a bookmark: %v", err)
	}
	if !strings.HasPrefix(string(token), "p\x00") {
		t.Errorf("token kind = %q, want plain path outside the sandbox", token[0])
	}

	// A second Connect swaps the target and must not fail while
	// releasing the previous grant.
	other := t.TempDir()
	writeSQLiteMigrations(t, other)
	_, err = svc.Connect(dbURL, other)
	if err != nil {
		t.Fatalf("second Connect failed: %v", err)
	}
}
