package introspect

import (
	"context"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// testDB connects to a database of this package's own, created on the
// fly. Read models EVERY schema, public included, so sharing
// DATABASE_URL with another test package is a race: `go test ./...`
// runs packages concurrently, and core's PostgreSQL tests create and
// drop tables in public. That made TestReadDeterministic fail in CI -
// two reads straddling another package's DDL.
func testDB(t *testing.T) *sqlx.DB {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL environment variable not set, skipping PostgreSQL introspection test")
	}

	admin, err := sqlx.Connect("postgres", base)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() {
		_ = admin.Close()
	}()

	const name = "migration_introspect_test"
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

	db, err := sqlx.Connect("postgres", u.String())
	if err != nil {
		t.Fatalf("failed to connect to scratch database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

const fixtureSQL = `
CREATE SCHEMA introspect_test;
CREATE TYPE introspect_test.mood AS ENUM ('sad', 'ok', 'happy');
CREATE TABLE introspect_test.users (
	id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	name text NOT NULL,
	email text UNIQUE,
	mood introspect_test.mood DEFAULT 'ok',
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE introspect_test.posts (
	id serial PRIMARY KEY,
	user_id integer NOT NULL REFERENCES introspect_test.users(id) ON DELETE CASCADE,
	title text NOT NULL,
	views integer NOT NULL DEFAULT 0,
	CONSTRAINT views_nonneg CHECK (views >= 0)
);
CREATE INDEX idx_posts_title ON introspect_test.posts (title);
CREATE INDEX idx_posts_views_partial ON introspect_test.posts (views) WHERE views > 100;
CREATE SEQUENCE introspect_test.invoice_seq START 100 INCREMENT 5;
CREATE TABLE introspect_test.schema_migrations (version integer PRIMARY KEY);
`

func setupFixture(t *testing.T, db *sqlx.DB) {
	t.Helper()
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `DROP SCHEMA IF EXISTS introspect_test CASCADE`)
	if err != nil {
		t.Fatalf("failed to drop leftover fixture schema: %v", err)
	}
	_, err = db.ExecContext(ctx, fixtureSQL)
	if err != nil {
		t.Fatalf("failed to create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, cleanupErr := db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS introspect_test CASCADE`)
		if cleanupErr != nil {
			t.Logf("Warning: failed to drop fixture schema: %v", cleanupErr)
		}
	})
}

func findTable(s *Schema, schemaName, name string) *Table {
	for i := range s.Tables {
		if s.Tables[i].Schema == schemaName && s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	return nil
}

func findColumn(t *Table, name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

func TestRead(t *testing.T) {
	db := testDB(t)
	setupFixture(t, db)
	ctx := context.Background()

	schema, err := Read(ctx, db)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if schema.Format != FormatVersion {
		t.Errorf("expected format %d, got %d", FormatVersion, schema.Format)
	}

	users := findTable(schema, "introspect_test", "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	id := findColumn(users, "id")
	if id == nil {
		t.Fatal("users.id column not found")
	}
	if id.Type != "integer" || !id.NotNull || id.Identity != "a" {
		t.Errorf("users.id = %+v, want integer, not null, identity always", id)
	}

	name := findColumn(users, "name")
	if name == nil || name.Type != "text" || !name.NotNull {
		t.Errorf("users.name = %+v, want text not null", name)
	}

	mood := findColumn(users, "mood")
	if mood == nil {
		t.Fatal("users.mood column not found")
	}
	if mood.Type != "introspect_test.mood" {
		t.Errorf("users.mood type = %q, want introspect_test.mood", mood.Type)
	}
	if !strings.Contains(mood.Default, "'ok'") {
		t.Errorf("users.mood default = %q, want it to contain 'ok'", mood.Default)
	}

	if users.PrimaryKey == nil || !strings.Contains(users.PrimaryKey.Definition, "PRIMARY KEY") {
		t.Errorf("users primary key = %+v, want PRIMARY KEY definition", users.PrimaryKey)
	}
	if len(users.Uniques) != 1 || !strings.Contains(users.Uniques[0].Definition, "UNIQUE") {
		t.Errorf("users uniques = %+v, want one UNIQUE constraint", users.Uniques)
	}

	posts := findTable(schema, "introspect_test", "posts")
	if posts == nil {
		t.Fatal("posts table not found")
	}

	postsID := findColumn(posts, "id")
	if postsID == nil || !strings.Contains(postsID.Default, "nextval") {
		t.Errorf("posts.id = %+v, want serial default nextval", postsID)
	}

	if len(posts.ForeignKeys) != 1 || !strings.Contains(posts.ForeignKeys[0].Definition, "REFERENCES introspect_test.users(id)") {
		t.Errorf("posts foreign keys = %+v, want one REFERENCES users", posts.ForeignKeys)
	}
	if len(posts.Checks) != 1 || !strings.Contains(posts.Checks[0].Definition, "views >= 0") {
		t.Errorf("posts checks = %+v, want views >= 0", posts.Checks)
	}

	if len(posts.Indexes) != 2 {
		t.Fatalf("posts indexes = %+v, want 2 (constraint-backing indexes must be excluded)", posts.Indexes)
	}
	if posts.Indexes[0].Name != "idx_posts_title" {
		t.Errorf("first posts index = %q, want idx_posts_title", posts.Indexes[0].Name)
	}
	if !strings.Contains(posts.Indexes[1].Definition, "WHERE") {
		t.Errorf("partial index definition = %q, want WHERE clause preserved", posts.Indexes[1].Definition)
	}

	if findTable(schema, "introspect_test", "schema_migrations") != nil {
		t.Error("schema_migrations must be excluded from the model")
	}

	foundEnum := false
	for _, e := range schema.Enums {
		if e.Schema == "introspect_test" && e.Name == "mood" {
			foundEnum = true
			want := []string{"sad", "ok", "happy"}
			if !reflect.DeepEqual(e.Values, want) {
				t.Errorf("mood values = %v, want %v (sort order preserved)", e.Values, want)
			}
		}
	}
	if !foundEnum {
		t.Error("mood enum not found")
	}

	foundSeq := false
	for _, s := range schema.Sequences {
		if s.Schema == "introspect_test" && s.Name == "invoice_seq" {
			foundSeq = true
			if s.Start != 100 || s.Increment != 5 {
				t.Errorf("invoice_seq = %+v, want start 100 increment 5", s)
			}
		}
		if s.Schema == "introspect_test" && strings.Contains(s.Name, "posts_id") {
			t.Errorf("serial-owned sequence %s must be excluded", s.Name)
		}
	}
	if !foundSeq {
		t.Error("invoice_seq sequence not found")
	}
}

func TestReadDeterministic(t *testing.T) {
	db := testDB(t)
	setupFixture(t, db)
	ctx := context.Background()

	first, err := Read(ctx, db)
	if err != nil {
		t.Fatalf("first Read failed: %v", err)
	}
	second, err := Read(ctx, db)
	if err != nil {
		t.Fatalf("second Read failed: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Error("two reads of the same schema produced different models")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	db := testDB(t)
	setupFixture(t, db)
	ctx := context.Background()

	schema, err := Read(ctx, db)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	data, err := schema.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	loaded, err := Load(data)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !reflect.DeepEqual(schema, loaded) {
		t.Error("JSON round trip changed the model")
	}
}

func TestLoadRejectsNewerFormat(t *testing.T) {
	_, err := Load([]byte(`{"format": 999}`))
	if err == nil {
		t.Fatal("expected error for newer snapshot format")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadRejectsGarbage(t *testing.T) {
	_, err := Load([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadRejectsDuplicateKeys(t *testing.T) {
	// Snapshots are hand-editable; json/v2 rejects a duplicated member
	// instead of silently keeping the last one.
	_, err := Load([]byte(`{"format": 1, "format": 2}`))
	if err == nil {
		t.Fatal("expected error for duplicate object member")
	}
}

func TestToJSONKeepsDDLReadable(t *testing.T) {
	s := &Schema{
		Format: FormatVersion,
		Tables: []Table{
			{Schema: "public", Name: "users", Checks: []Constraint{
				{Name: "name_ok", Definition: "CHECK ((name <> ''::text) AND (id > 0))"},
			}},
		},
	}
	data, err := s.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	if !strings.Contains(string(data), "name <> ''::text") {
		t.Errorf("DDL must not be HTML-escaped in snapshots:\n%s", data)
	}
}
