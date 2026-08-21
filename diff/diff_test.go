package diff

import (
	"strings"
	"testing"

	"github.com/crgimenes/migration/introspect"
)

func baseSchema() *introspect.Schema {
	return &introspect.Schema{
		Format: introspect.FormatVersion,
		Tables: []introspect.Table{
			{
				Schema: "public",
				Name:   "users",
				Columns: []introspect.Column{
					{Name: "id", Type: "integer", NotNull: true, Identity: "a"},
					{Name: "name", Type: "text", NotNull: true},
				},
				PrimaryKey: &introspect.Constraint{Name: "users_pkey", Definition: "PRIMARY KEY (id)"},
				Indexes: []introspect.Index{
					{Name: "idx_users_name", Definition: "CREATE INDEX idx_users_name ON public.users USING btree (name)"},
				},
			},
		},
		Enums: []introspect.Enum{
			{Schema: "public", Name: "mood", Values: []string{"sad", "ok"}},
		},
		Sequences: []introspect.Sequence{
			{Schema: "public", Name: "invoice_seq", Start: 100, Increment: 5, Min: 1, Max: 1000, Cycle: false},
		},
	}
}

func kinds(changes []Change) []Kind {
	out := make([]Kind, len(changes))
	for i, c := range changes {
		out[i] = c.Kind
	}
	return out
}

func TestCompareIdentical(t *testing.T) {
	changes := Compare(baseSchema(), baseSchema())
	if len(changes) != 0 {
		t.Errorf("identical schemas produced changes: %v", changes)
	}
}

func TestCompareTableAddedAndDropped(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Tables = append(b.Tables, introspect.Table{Schema: "public", Name: "posts"})

	changes := Compare(a, b)
	if len(changes) != 1 || changes[0].Kind != TableAdded || changes[0].Object != "posts" {
		t.Fatalf("expected one table_added posts, got %v", changes)
	}

	reverse := Compare(b, a)
	if len(reverse) != 1 || reverse[0].Kind != TableDropped {
		t.Fatalf("expected one table_dropped, got %v", reverse)
	}
}

func TestCompareColumnChanges(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Tables[0].Columns[1].Type = "varchar(120)"
	b.Tables[0].Columns = append(b.Tables[0].Columns, introspect.Column{Name: "age", Type: "integer"})

	changes := Compare(a, b)
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %v", changes)
	}
	if changes[0].Kind != ColumnAltered || changes[0].Object != "name" {
		t.Errorf("expected column_altered name, got %v", changes[0])
	}
	if changes[0].Old != "text not null" || changes[0].New != "varchar(120) not null" {
		t.Errorf("column_altered old/new = %q / %q", changes[0].Old, changes[0].New)
	}
	if changes[1].Kind != ColumnAdded || changes[1].Object != "age" {
		t.Errorf("expected column_added age, got %v", changes[1])
	}
}

func TestCompareConstraintAndIndexChanges(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Tables[0].Checks = append(b.Tables[0].Checks, introspect.Constraint{Name: "age_positive", Definition: "CHECK (age > 0)"})
	b.Tables[0].Indexes[0].Definition = "CREATE INDEX idx_users_name ON public.users USING btree (lower(name))"

	changes := Compare(a, b)
	got := kinds(changes)
	want := []Kind{ConstraintAdded, IndexAltered}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, changes)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestComparePrimaryKeyDropped(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Tables[0].PrimaryKey = nil

	changes := Compare(a, b)
	if len(changes) != 1 || changes[0].Kind != ConstraintDropped || changes[0].Object != "users_pkey" {
		t.Fatalf("expected constraint_dropped users_pkey, got %v", changes)
	}
}

func TestCompareEnumValueAppended(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Enums[0].Values = []string{"sad", "ok", "happy", "great"}

	changes := Compare(a, b)
	if len(changes) != 2 {
		t.Fatalf("expected 2 enum_value_added, got %v", changes)
	}
	if changes[0].Kind != EnumValueAdded || changes[0].New != "happy" {
		t.Errorf("expected enum_value_added happy, got %v", changes[0])
	}
	if changes[1].Kind != EnumValueAdded || changes[1].New != "great" {
		t.Errorf("expected enum_value_added great, got %v", changes[1])
	}
}

func TestCompareEnumReordered(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Enums[0].Values = []string{"ok", "sad"}

	changes := Compare(a, b)
	if len(changes) != 1 || changes[0].Kind != EnumAltered {
		t.Fatalf("expected enum_altered, got %v", changes)
	}
	if !strings.Contains(changes[0].Note, "recreating the type") {
		t.Errorf("enum_altered note = %q, want recreate warning", changes[0].Note)
	}
}

func TestCompareSequences(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Sequences[0].Increment = 10
	b.Sequences = append(b.Sequences, introspect.Sequence{Schema: "public", Name: "order_seq", Start: 1, Increment: 1})

	changes := Compare(a, b)
	got := kinds(changes)
	want := []Kind{SequenceAltered, SequenceAdded}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected %v, got %v", want, changes)
	}
}

func TestInverse(t *testing.T) {
	tests := []struct {
		name string
		in   Change
		want Change
	}{
		{
			"added becomes dropped",
			Change{Kind: ColumnAdded, Schema: "public", Table: "users", Object: "age", New: "integer"},
			Change{Kind: ColumnDropped, Schema: "public", Table: "users", Object: "age", Old: "integer"},
		},
		{
			"altered swaps old and new",
			Change{Kind: ColumnAltered, Object: "name", Old: "text", New: "varchar(120)"},
			Change{Kind: ColumnAltered, Object: "name", Old: "varchar(120)", New: "text"},
		},
		{
			"table dropped becomes added",
			Change{Kind: TableDropped, Schema: "public", Object: "users"},
			Change{Kind: TableAdded, Schema: "public", Object: "users"},
		},
		{
			"enum value added inverts to altered",
			Change{Kind: EnumValueAdded, Schema: "public", Object: "mood", New: "happy"},
			Change{Kind: EnumAltered, Schema: "public", Object: "mood", Old: "happy"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.Inverse()
			if got != tt.want {
				t.Errorf("Inverse() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInverseRoundTrip(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Tables[0].Columns = append(b.Tables[0].Columns, introspect.Column{Name: "age", Type: "integer"})
	b.Enums[0].Values = append(b.Enums[0].Values, "happy")

	forward := Compare(a, b)
	backward := Compare(b, a)
	if len(forward) != len(backward) {
		t.Fatalf("forward %d changes, backward %d", len(forward), len(backward))
	}
}

func TestPossibleRenameColumn(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Tables[0].Columns[1].Name = "full_name"

	changes := Compare(a, b)
	if len(changes) != 2 {
		t.Fatalf("expected drop+add pair, got %v", changes)
	}
	for _, c := range changes {
		if !strings.Contains(c.Note, "possible rename") {
			t.Errorf("change %v missing possible-rename note", c)
		}
		if !strings.Contains(c.Note, "RENAME COLUMN") {
			t.Errorf("change %v note should point at RENAME COLUMN", c)
		}
	}
}

func TestPossibleRenameTable(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Tables[0].Name = "accounts"

	changes := Compare(a, b)
	if len(changes) != 2 {
		t.Fatalf("expected drop+add pair, got %v", changes)
	}
	for _, c := range changes {
		if !strings.Contains(c.Note, "possible rename") {
			t.Errorf("change %v missing possible-rename note", c)
		}
	}
}

func TestNoRenameNoteWhenTypesDiffer(t *testing.T) {
	a := baseSchema()
	b := baseSchema()
	b.Tables[0].Columns[1] = introspect.Column{Name: "age", Type: "integer"}

	changes := Compare(a, b)
	for _, c := range changes {
		if c.Kind == ColumnDropped || c.Kind == ColumnAdded {
			if c.Note != "" {
				t.Errorf("change %v should not be flagged as rename (definitions differ)", c)
			}
		}
	}
}

func TestSummarize(t *testing.T) {
	changes := []Change{
		{Kind: TableAdded},
		{Kind: ColumnAltered},
		{Kind: ColumnAltered},
		{Kind: EnumValueAdded},
		{Kind: IndexDropped},
	}
	got := Summarize(changes)
	want := "1 table added, 2 columns altered, 1 index dropped, 1 enum value added"
	if got != want {
		t.Errorf("Summarize() = %q, want %q", got, want)
	}

	if Summarize(nil) != "" {
		t.Errorf("Summarize(nil) = %q, want empty", Summarize(nil))
	}
}

func TestChangeString(t *testing.T) {
	c := Change{Kind: ColumnAltered, Schema: "public", Table: "users", Object: "age", Old: "integer", New: "bigint"}
	got := c.String()
	want := "column_altered public.users.age: integer -> bigint"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	c = Change{Kind: TableAdded, Schema: "public", Object: "posts"}
	got = c.String()
	want = "table_added public.posts"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
