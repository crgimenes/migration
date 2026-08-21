package gen

import (
	"strings"
	"testing"

	"github.com/crgimenes/migration/diff"
	"github.com/crgimenes/migration/introspect"
)

func emptySchema() *introspect.Schema {
	return &introspect.Schema{Format: introspect.FormatVersion}
}

func usersSchema() *introspect.Schema {
	return &introspect.Schema{
		Format: introspect.FormatVersion,
		Tables: []introspect.Table{
			{
				Schema: "public",
				Name:   "users",
				Columns: []introspect.Column{
					{Name: "id", Type: "integer", NotNull: true, Identity: "a"},
					{Name: "name", Type: "text", NotNull: true},
					{Name: "created_at", Type: "timestamp with time zone", NotNull: true, Default: "now()"},
				},
				PrimaryKey: &introspect.Constraint{Name: "users_pkey", Definition: "PRIMARY KEY (id)"},
				Uniques: []introspect.Constraint{
					{Name: "users_name_key", Definition: "UNIQUE (name)"},
				},
				Indexes: []introspect.Index{
					{Name: "idx_users_created", Definition: "CREATE INDEX idx_users_created ON public.users USING btree (created_at)"},
				},
			},
		},
	}
}

func TestGenerateCreateTable(t *testing.T) {
	changes := diff.Compare(emptySchema(), usersSchema())
	up, down := Generate(changes, emptySchema(), usersSchema())

	for _, want := range []string{
		"CREATE TABLE public.users (",
		"id integer NOT NULL GENERATED ALWAYS AS IDENTITY",
		"name text NOT NULL",
		"created_at timestamp with time zone NOT NULL DEFAULT now()",
		"CONSTRAINT users_pkey PRIMARY KEY (id)",
		"CONSTRAINT users_name_key UNIQUE (name)",
		"CREATE INDEX idx_users_created ON public.users USING btree (created_at);",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up missing %q:\n%s", want, up)
		}
	}

	if !strings.Contains(down, "-- DROP TABLE public.users;") {
		t.Errorf("down should contain commented destructive drop:\n%s", down)
	}
	if !strings.Contains(down, "-- DESTRUCTIVE:") {
		t.Errorf("down missing destructive warning:\n%s", down)
	}
}

func TestGenerateForeignKeyOrdering(t *testing.T) {
	to := usersSchema()
	to.Tables = append([]introspect.Table{
		{
			Schema: "public",
			Name:   "aardvark_posts",
			Columns: []introspect.Column{
				{Name: "id", Type: "integer", NotNull: true},
				{Name: "user_id", Type: "integer", NotNull: true},
			},
			ForeignKeys: []introspect.Constraint{
				{Name: "posts_user_fk", Definition: "FOREIGN KEY (user_id) REFERENCES public.users(id)"},
			},
		},
	}, to.Tables...)

	changes := diff.Compare(emptySchema(), to)
	up, _ := Generate(changes, emptySchema(), to)

	fkPos := strings.Index(up, "ADD CONSTRAINT posts_user_fk")
	usersPos := strings.Index(up, "CREATE TABLE public.users")
	postsPos := strings.Index(up, "CREATE TABLE public.aardvark_posts")
	if fkPos == -1 || usersPos == -1 || postsPos == -1 {
		t.Fatalf("missing statements in up:\n%s", up)
	}
	if fkPos < usersPos || fkPos < postsPos {
		t.Errorf("foreign key must come after both CREATE TABLE statements:\n%s", up)
	}
}

func TestGenerateColumnChanges(t *testing.T) {
	from := usersSchema()
	to := usersSchema()
	to.Tables[0].Columns[1].Type = "character varying(120)"
	to.Tables[0].Columns[1].NotNull = false
	to.Tables[0].Columns[2].Default = ""
	to.Tables[0].Columns = append(to.Tables[0].Columns, introspect.Column{Name: "age", Type: "integer", Default: "0"})

	changes := diff.Compare(from, to)
	up, down := Generate(changes, from, to)

	for _, want := range []string{
		"ALTER TABLE public.users ALTER COLUMN name TYPE character varying(120);",
		"ALTER TABLE public.users ALTER COLUMN name DROP NOT NULL;",
		"ALTER TABLE public.users ALTER COLUMN created_at DROP DEFAULT;",
		"ALTER TABLE public.users ADD COLUMN age integer DEFAULT 0;",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up missing %q:\n%s", want, up)
		}
	}

	for _, want := range []string{
		"ALTER TABLE public.users ALTER COLUMN name TYPE text;",
		"ALTER TABLE public.users ALTER COLUMN name SET NOT NULL;",
		"ALTER TABLE public.users ALTER COLUMN created_at SET DEFAULT now();",
		"-- ALTER TABLE public.users DROP COLUMN age;",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down missing %q:\n%s", want, down)
		}
	}
}

func TestGenerateIndexAndConstraint(t *testing.T) {
	from := usersSchema()
	to := usersSchema()
	to.Tables[0].Indexes[0].Definition = "CREATE INDEX idx_users_created ON public.users USING btree (created_at DESC)"
	to.Tables[0].Checks = []introspect.Constraint{
		{Name: "name_not_empty", Definition: "CHECK (name <> '')"},
	}

	changes := diff.Compare(from, to)
	up, down := Generate(changes, from, to)

	for _, want := range []string{
		"DROP INDEX public.idx_users_created;",
		"CREATE INDEX idx_users_created ON public.users USING btree (created_at DESC);",
		"ALTER TABLE public.users ADD CONSTRAINT name_not_empty CHECK (name <> '');",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up missing %q:\n%s", want, up)
		}
	}

	if !strings.Contains(down, "ALTER TABLE public.users DROP CONSTRAINT name_not_empty;") {
		t.Errorf("down missing constraint drop:\n%s", down)
	}
	if !strings.Contains(down, "CREATE INDEX idx_users_created ON public.users USING btree (created_at)") {
		t.Errorf("down missing old index restore:\n%s", down)
	}
}

func TestGenerateEnumAndSequence(t *testing.T) {
	from := emptySchema()
	to := emptySchema()
	to.Enums = []introspect.Enum{{Schema: "public", Name: "mood", Values: []string{"sad", "it's ok"}}}
	to.Sequences = []introspect.Sequence{{Schema: "public", Name: "invoice_seq", Start: 100, Increment: 5, Min: 1, Max: 9999, Cycle: false}}

	changes := diff.Compare(from, to)
	up, down := Generate(changes, from, to)

	if !strings.Contains(up, "CREATE TYPE public.mood AS ENUM ('sad', 'it''s ok');") {
		t.Errorf("up missing enum create (with escaped quote):\n%s", up)
	}
	if !strings.Contains(up, "CREATE SEQUENCE public.invoice_seq START 100 INCREMENT 5 MINVALUE 1 MAXVALUE 9999 NO CYCLE;") {
		t.Errorf("up missing sequence create:\n%s", up)
	}
	if !strings.Contains(down, "-- DROP TYPE public.mood;") {
		t.Errorf("down missing commented enum drop:\n%s", down)
	}
	if !strings.Contains(down, "-- DROP SEQUENCE public.invoice_seq;") {
		t.Errorf("down missing commented sequence drop:\n%s", down)
	}
}

func TestGenerateEnumValueAdded(t *testing.T) {
	from := emptySchema()
	from.Enums = []introspect.Enum{{Schema: "public", Name: "mood", Values: []string{"sad"}}}
	to := emptySchema()
	to.Enums = []introspect.Enum{{Schema: "public", Name: "mood", Values: []string{"sad", "happy"}}}

	changes := diff.Compare(from, to)
	up, down := Generate(changes, from, to)

	if !strings.Contains(up, "ALTER TYPE public.mood ADD VALUE 'happy';") {
		t.Errorf("up missing add value:\n%s", up)
	}
	if !strings.Contains(down, "-- MANUAL: enum public.mood") {
		t.Errorf("down must flag manual enum handling (PostgreSQL cannot drop a value):\n%s", down)
	}
}

func TestGenerateEmpty(t *testing.T) {
	up, down := Generate(nil, emptySchema(), emptySchema())
	if up != "" || down != "" {
		t.Errorf("empty change list must produce empty SQL, got up=%q down=%q", up, down)
	}
}

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"users", "users"},
		{"user_accounts2", "user_accounts2"},
		{"Users", `"Users"`},
		{"weird name", `"weird name"`},
		{`has"quote`, `"has""quote"`},
	}
	for _, tt := range tests {
		got := quoteIdent(tt.in)
		if got != tt.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
