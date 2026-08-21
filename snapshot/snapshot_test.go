package snapshot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/crgimenes/migration/introspect"
)

func model(tableName string) *introspect.Schema {
	return &introspect.Schema{
		Format: introspect.FormatVersion,
		Tables: []introspect.Table{
			{Schema: "public", Name: tableName, Columns: []introspect.Column{
				{Name: "id", Type: "integer", NotNull: true},
			}},
		},
	}
}

func TestWriteLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	path, err := Write(dir, 3, model("users"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if path != filepath.Join(dir, "snapshots", "003.json") {
		t.Errorf("unexpected snapshot path %q", path)
	}

	loaded, err := Load(dir, 3)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !reflect.DeepEqual(loaded, model("users")) {
		t.Error("round trip changed the model")
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(t.TempDir(), 1)
	if err == nil {
		t.Fatal("expected error loading missing snapshot")
	}
}

func TestVersionsAndLatest(t *testing.T) {
	dir := t.TempDir()

	for _, v := range []int{2, 10, 1} {
		_, err := Write(dir, v, model("users"))
		if err != nil {
			t.Fatalf("Write %d failed: %v", v, err)
		}
	}
	err := os.WriteFile(filepath.Join(Dir(dir), "notes.json"), []byte("{}"), 0o644)
	if err != nil {
		t.Fatalf("failed to write stray file: %v", err)
	}

	versions, err := Versions(dir)
	if err != nil {
		t.Fatalf("Versions failed: %v", err)
	}
	want := []int{1, 2, 10}
	if !reflect.DeepEqual(versions, want) {
		t.Errorf("Versions = %v, want %v (sorted, stray file ignored)", versions, want)
	}

	last, s, found, err := Latest(dir)
	if err != nil {
		t.Fatalf("Latest failed: %v", err)
	}
	if !found || last != 10 || s == nil {
		t.Errorf("Latest = (%d, %v, %v), want version 10", last, s, found)
	}
}

func TestLatestEmpty(t *testing.T) {
	_, _, found, err := Latest(t.TempDir())
	if err != nil {
		t.Fatalf("Latest failed: %v", err)
	}
	if found {
		t.Error("Latest on empty dir must report not found")
	}
}
