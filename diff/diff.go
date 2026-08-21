// Package diff compares two introspected schema models and produces a
// typed change list. Pure functions: no database access, so the engine
// is unit-testable and reusable by capture, report and the GUI.
package diff

import (
	"fmt"
	"strings"

	"github.com/crgimenes/migration/introspect"
)

type Kind string

const (
	TableAdded   Kind = "table_added"
	TableDropped Kind = "table_dropped"

	ColumnAdded   Kind = "column_added"
	ColumnDropped Kind = "column_dropped"
	ColumnAltered Kind = "column_altered"

	IndexAdded   Kind = "index_added"
	IndexDropped Kind = "index_dropped"
	IndexAltered Kind = "index_altered"

	ConstraintAdded   Kind = "constraint_added"
	ConstraintDropped Kind = "constraint_dropped"
	ConstraintAltered Kind = "constraint_altered"

	EnumAdded      Kind = "enum_added"
	EnumDropped    Kind = "enum_dropped"
	EnumValueAdded Kind = "enum_value_added"
	// EnumAltered means values were removed or reordered, which
	// PostgreSQL cannot express as ALTER TYPE; fixing it needs a
	// type recreate and is always flagged for human review.
	EnumAltered Kind = "enum_altered"

	SequenceAdded   Kind = "sequence_added"
	SequenceDropped Kind = "sequence_dropped"
	SequenceAltered Kind = "sequence_altered"
)

// Change identifies one difference between two schemas. Old and New
// carry the compared textual forms (type, definition, value) so a
// reader does not need to chase the models to see what moved; the DDL
// generator receives the two schemas alongside the changes for full
// lookups.
type Change struct {
	Kind   Kind   `json:"kind"`
	Schema string `json:"schema"`
	Table  string `json:"table,omitempty"`
	Object string `json:"object"`
	Old    string `json:"old,omitempty"`
	New    string `json:"new,omitempty"`
	Note   string `json:"note,omitempty"`
}

// String renders one change for humans: "column_altered public.users.age: integer -> bigint".
func (c Change) String() string {
	place := c.Schema + "." + c.Object
	if c.Table != "" {
		place = c.Schema + "." + c.Table + "." + c.Object
	}
	s := fmt.Sprintf("%s %s", c.Kind, place)
	if c.Old != "" || c.New != "" {
		s += fmt.Sprintf(": %s -> %s", c.Old, c.New)
	}
	if c.Note != "" {
		s += " (" + c.Note + ")"
	}
	return s
}

var inverseKind = map[Kind]Kind{
	TableAdded:        TableDropped,
	TableDropped:      TableAdded,
	ColumnAdded:       ColumnDropped,
	ColumnDropped:     ColumnAdded,
	ColumnAltered:     ColumnAltered,
	IndexAdded:        IndexDropped,
	IndexDropped:      IndexAdded,
	IndexAltered:      IndexAltered,
	ConstraintAdded:   ConstraintDropped,
	ConstraintDropped: ConstraintAdded,
	ConstraintAltered: ConstraintAltered,
	EnumAdded:         EnumDropped,
	EnumDropped:       EnumAdded,
	EnumValueAdded:    EnumAltered,
	EnumAltered:       EnumAltered,
	SequenceAdded:     SequenceDropped,
	SequenceDropped:   SequenceAdded,
	SequenceAltered:   SequenceAltered,
}

// Inverse returns the change that undoes this one; altered kinds swap
// old and new. This is what makes generated down migrations possible.
func (c Change) Inverse() Change {
	inv := c
	inv.Kind = inverseKind[c.Kind]
	inv.Old, inv.New = c.New, c.Old
	return inv
}

// Compare returns the changes that turn schema a into schema b, in
// deterministic order (model order, which is sorted by the reader).
func Compare(a, b *introspect.Schema) []Change {
	var changes []Change
	changes = append(changes, compareTables(a, b)...)
	changes = append(changes, compareEnums(a, b)...)
	changes = append(changes, compareSequences(a, b)...)
	flagPossibleRenames(changes)
	return changes
}

func tableKey(t introspect.Table) string {
	return t.Schema + "." + t.Name
}

func compareTables(a, b *introspect.Schema) []Change {
	var changes []Change

	oldTables := map[string]*introspect.Table{}
	for i := range a.Tables {
		oldTables[tableKey(a.Tables[i])] = &a.Tables[i]
	}
	newTables := map[string]*introspect.Table{}
	for i := range b.Tables {
		newTables[tableKey(b.Tables[i])] = &b.Tables[i]
	}

	for i := range a.Tables {
		old := &a.Tables[i]
		if newTables[tableKey(*old)] == nil {
			changes = append(changes, Change{Kind: TableDropped, Schema: old.Schema, Object: old.Name})
		}
	}
	for i := range b.Tables {
		fresh := &b.Tables[i]
		old := oldTables[tableKey(*fresh)]
		if old == nil {
			changes = append(changes, Change{Kind: TableAdded, Schema: fresh.Schema, Object: fresh.Name})
			continue
		}
		changes = append(changes, compareOneTable(old, fresh)...)
	}
	return changes
}

func columnText(c introspect.Column) string {
	s := c.Type
	if c.NotNull {
		s += " not null"
	}
	if c.Default != "" {
		s += " default " + c.Default
	}
	if c.Identity != "" {
		s += " identity " + c.Identity
	}
	if c.Generated != "" {
		s += " generated " + c.Generated
	}
	return s
}

func compareOneTable(old, fresh *introspect.Table) []Change {
	var changes []Change

	oldCols := map[string]introspect.Column{}
	for _, c := range old.Columns {
		oldCols[c.Name] = c
	}
	newCols := map[string]introspect.Column{}
	for _, c := range fresh.Columns {
		newCols[c.Name] = c
	}

	for _, c := range old.Columns {
		_, ok := newCols[c.Name]
		if !ok {
			changes = append(changes, Change{Kind: ColumnDropped, Schema: old.Schema, Table: old.Name, Object: c.Name, Old: columnText(c)})
		}
	}
	for _, c := range fresh.Columns {
		before, ok := oldCols[c.Name]
		if !ok {
			changes = append(changes, Change{Kind: ColumnAdded, Schema: fresh.Schema, Table: fresh.Name, Object: c.Name, New: columnText(c)})
			continue
		}
		if before != c {
			changes = append(changes, Change{Kind: ColumnAltered, Schema: fresh.Schema, Table: fresh.Name, Object: c.Name, Old: columnText(before), New: columnText(c)})
		}
	}

	changes = append(changes, compareConstraints(old, fresh)...)
	changes = append(changes, compareIndexes(old, fresh)...)
	return changes
}

func constraintList(t *introspect.Table) []introspect.Constraint {
	var all []introspect.Constraint
	if t.PrimaryKey != nil {
		all = append(all, *t.PrimaryKey)
	}
	all = append(all, t.ForeignKeys...)
	all = append(all, t.Uniques...)
	all = append(all, t.Checks...)
	return all
}

func compareConstraints(old, fresh *introspect.Table) []Change {
	return compareNamed(
		old.Schema, old.Name,
		constraintList(old), constraintList(fresh),
		func(c introspect.Constraint) (string, string) { return c.Name, c.Definition },
		ConstraintAdded, ConstraintDropped, ConstraintAltered,
	)
}

func compareIndexes(old, fresh *introspect.Table) []Change {
	return compareNamed(
		old.Schema, old.Name,
		old.Indexes, fresh.Indexes,
		func(i introspect.Index) (string, string) { return i.Name, i.Definition },
		IndexAdded, IndexDropped, IndexAltered,
	)
}

// compareNamed diffs two lists of named objects whose whole identity is
// a definition string (constraints, indexes).
func compareNamed[T any](schemaName, tableName string, old, fresh []T, id func(T) (name, definition string), added, dropped, altered Kind) []Change {
	var changes []Change

	oldDefs := map[string]string{}
	for _, o := range old {
		name, def := id(o)
		oldDefs[name] = def
	}
	newDefs := map[string]string{}
	for _, o := range fresh {
		name, def := id(o)
		newDefs[name] = def
	}

	for _, o := range old {
		name, def := id(o)
		_, ok := newDefs[name]
		if !ok {
			changes = append(changes, Change{Kind: dropped, Schema: schemaName, Table: tableName, Object: name, Old: def})
		}
	}
	for _, o := range fresh {
		name, def := id(o)
		before, ok := oldDefs[name]
		if !ok {
			changes = append(changes, Change{Kind: added, Schema: schemaName, Table: tableName, Object: name, New: def})
			continue
		}
		if before != def {
			changes = append(changes, Change{Kind: altered, Schema: schemaName, Table: tableName, Object: name, Old: before, New: def})
		}
	}
	return changes
}

func enumKey(e introspect.Enum) string {
	return e.Schema + "." + e.Name
}

func enumValuesText(values []string) string {
	var s strings.Builder
	for i, v := range values {
		if i > 0 {
			s.WriteString(", ")
		}
		s.WriteString(v)
	}
	return s.String()
}

func compareEnums(a, b *introspect.Schema) []Change {
	var changes []Change

	oldEnums := map[string]introspect.Enum{}
	for _, e := range a.Enums {
		oldEnums[enumKey(e)] = e
	}
	newEnums := map[string]introspect.Enum{}
	for _, e := range b.Enums {
		newEnums[enumKey(e)] = e
	}

	for _, e := range a.Enums {
		_, ok := newEnums[enumKey(e)]
		if !ok {
			changes = append(changes, Change{Kind: EnumDropped, Schema: e.Schema, Object: e.Name, Old: enumValuesText(e.Values)})
		}
	}
	for _, e := range b.Enums {
		before, ok := oldEnums[enumKey(e)]
		if !ok {
			changes = append(changes, Change{Kind: EnumAdded, Schema: e.Schema, Object: e.Name, New: enumValuesText(e.Values)})
			continue
		}
		changes = append(changes, compareEnumValues(before, e)...)
	}
	return changes
}

func compareEnumValues(before, after introspect.Enum) []Change {
	// Appending values is the only enum change ALTER TYPE can express;
	// anything else (removal, reorder) is a type recreate.
	appendOnly := len(after.Values) >= len(before.Values)
	if appendOnly {
		for i, v := range before.Values {
			if after.Values[i] != v {
				appendOnly = false
				break
			}
		}
	}
	if !appendOnly {
		return []Change{{
			Kind:   EnumAltered,
			Schema: after.Schema,
			Object: after.Name,
			Old:    enumValuesText(before.Values),
			New:    enumValuesText(after.Values),
			Note:   "values removed or reordered; requires recreating the type",
		}}
	}

	var changes []Change
	for _, v := range after.Values[len(before.Values):] {
		changes = append(changes, Change{Kind: EnumValueAdded, Schema: after.Schema, Object: after.Name, New: v})
	}
	return changes
}

func sequenceKey(s introspect.Sequence) string {
	return s.Schema + "." + s.Name
}

func sequenceText(s introspect.Sequence) string {
	return fmt.Sprintf("start %d increment %d min %d max %d cycle %v", s.Start, s.Increment, s.Min, s.Max, s.Cycle)
}

func compareSequences(a, b *introspect.Schema) []Change {
	var changes []Change

	oldSeqs := map[string]introspect.Sequence{}
	for _, s := range a.Sequences {
		oldSeqs[sequenceKey(s)] = s
	}
	newSeqs := map[string]introspect.Sequence{}
	for _, s := range b.Sequences {
		newSeqs[sequenceKey(s)] = s
	}

	for _, s := range a.Sequences {
		_, ok := newSeqs[sequenceKey(s)]
		if !ok {
			changes = append(changes, Change{Kind: SequenceDropped, Schema: s.Schema, Object: s.Name, Old: sequenceText(s)})
		}
	}
	for _, s := range b.Sequences {
		before, ok := oldSeqs[sequenceKey(s)]
		if !ok {
			changes = append(changes, Change{Kind: SequenceAdded, Schema: s.Schema, Object: s.Name, New: sequenceText(s)})
			continue
		}
		if before != s {
			changes = append(changes, Change{Kind: SequenceAltered, Schema: s.Schema, Object: s.Name, Old: sequenceText(before), New: sequenceText(s)})
		}
	}
	return changes
}

// flagPossibleRenames marks drop+add pairs that look like renames.
// Renames are never guessed silently: a wrong guess generates DROP of
// real data. The note tells the reviewer to rewrite the pair as
// ALTER ... RENAME before committing if it really is one.
func flagPossibleRenames(changes []Change) {
	type slot struct {
		schema, table, text string
	}

	droppedColumns := map[slot][]int{}
	addedColumns := map[slot][]int{}
	droppedTables := []int{}
	addedTables := []int{}

	for i, c := range changes {
		switch c.Kind {
		case ColumnDropped:
			key := slot{c.Schema, c.Table, c.Old}
			droppedColumns[key] = append(droppedColumns[key], i)
		case ColumnAdded:
			key := slot{c.Schema, c.Table, c.New}
			addedColumns[key] = append(addedColumns[key], i)
		case TableDropped:
			droppedTables = append(droppedTables, i)
		case TableAdded:
			addedTables = append(addedTables, i)
		}
	}

	for key, dropped := range droppedColumns {
		added := addedColumns[key]
		if len(dropped) == 0 || len(added) == 0 {
			continue
		}
		note := "possible rename: a dropped and an added column share the same definition; if it is a rename, rewrite as ALTER TABLE ... RENAME COLUMN"
		for _, i := range dropped {
			changes[i].Note = note
		}
		for _, i := range added {
			changes[i].Note = note
		}
	}

	if len(droppedTables) > 0 && len(addedTables) > 0 {
		note := "possible rename: tables dropped and added in the same diff; if it is a rename, rewrite as ALTER TABLE ... RENAME TO"
		for _, i := range droppedTables {
			changes[i].Note = note
		}
		for _, i := range addedTables {
			changes[i].Note = note
		}
	}
}
