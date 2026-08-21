// Package gen turns a diff change list into PostgreSQL DDL. Up comes
// from the changes, down from their inverses. Statements are emitted in
// dependency order (types and tables before the things that reference
// them, foreign keys last), and destructive statements are generated
// commented out so applying a file blindly can never destroy data.
package gen

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/crgimenes/migration/diff"
	"github.com/crgimenes/migration/introspect"
)

// Generate renders the up and down SQL for a change list that turns
// schema from into schema to.
func Generate(changes []diff.Change, from, to *introspect.Schema) (up, down string) {
	up = render(changes, from, to)

	inverse := make([]diff.Change, len(changes))
	for i, c := range changes {
		inverse[i] = c.Inverse()
	}
	down = render(inverse, to, from)
	return up, down
}

// bucket orders statements so that referenced objects exist before
// their dependents; destructive drops come last.
func bucket(c diff.Change) int {
	switch c.Kind {
	case diff.EnumAdded, diff.EnumValueAdded, diff.EnumAltered:
		return 0
	case diff.SequenceAdded, diff.SequenceAltered:
		return 1
	case diff.TableAdded:
		return 2
	case diff.ColumnAdded, diff.ColumnAltered:
		return 3
	case diff.ConstraintAdded, diff.ConstraintAltered, diff.IndexAdded, diff.IndexAltered:
		return 4
	case diff.IndexDropped, diff.ConstraintDropped:
		return 5
	default: // column/table/enum/sequence drops, all destructive
		return 6
	}
}

func render(changes []diff.Change, from, to *introspect.Schema) string {
	var statements []string
	// Foreign keys reference tables that may themselves be created in
	// this batch, so every FK is deferred to the end of its bucket pass.
	var foreignKeys []string

	for b := range 7 {
		for _, c := range changes {
			if bucket(c) != b {
				continue
			}
			stmt, fk := statement(c, from, to)
			statements = append(statements, stmt...)
			foreignKeys = append(foreignKeys, fk...)
		}
		if b == 4 {
			statements = append(statements, foreignKeys...)
			foreignKeys = nil
		}
	}

	if len(statements) == 0 {
		return ""
	}
	return strings.Join(statements, "\n") + "\n"
}

// statement renders one change. The second return value carries ALTER
// TABLE ... ADD CONSTRAINT statements for foreign keys, which the
// caller defers until every table of the batch exists.
func statement(c diff.Change, from, to *introspect.Schema) (stmts, foreignKeys []string) {
	switch c.Kind {
	case diff.TableAdded:
		return tableAdded(c, to)
	case diff.TableDropped:
		return destructive(fmt.Sprintf("DROP TABLE %s;", qualified(c.Schema, c.Object)),
			"all rows of "+qualified(c.Schema, c.Object)+" will be lost"), nil
	case diff.ColumnAdded:
		return columnAdded(c, to), nil
	case diff.ColumnDropped:
		return destructive(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", qualified(c.Schema, c.Table), quoteIdent(c.Object)),
			"data in column "+c.Object+" will be lost"), nil
	case diff.ColumnAltered:
		return columnAltered(c, from, to), nil
	case diff.IndexAdded, diff.IndexAltered:
		return indexChanged(c, to), nil
	case diff.IndexDropped:
		return []string{fmt.Sprintf("DROP INDEX %s;", qualified(c.Schema, c.Object))}, nil
	case diff.ConstraintAdded, diff.ConstraintAltered:
		return constraintChanged(c, to)
	case diff.ConstraintDropped:
		return []string{fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", qualified(c.Schema, c.Table), quoteIdent(c.Object))}, nil
	case diff.EnumAdded:
		return enumAdded(c, to), nil
	case diff.EnumDropped:
		return destructive(fmt.Sprintf("DROP TYPE %s;", qualified(c.Schema, c.Object)),
			"columns using this type must be handled first"), nil
	case diff.EnumValueAdded:
		return []string{fmt.Sprintf("ALTER TYPE %s ADD VALUE %s;", qualified(c.Schema, c.Object), quoteLiteral(c.New))}, nil
	case diff.EnumAltered:
		return []string{
			"-- MANUAL: enum " + qualified(c.Schema, c.Object) + " had values removed or reordered.",
			"-- PostgreSQL cannot express this as ALTER TYPE; recreate the type by hand:",
			"--   old: " + c.Old,
			"--   new: " + c.New,
		}, nil
	case diff.SequenceAdded:
		return sequenceAdded(c, to), nil
	case diff.SequenceDropped:
		return destructive(fmt.Sprintf("DROP SEQUENCE %s;", qualified(c.Schema, c.Object)),
			"the sequence position will be lost"), nil
	case diff.SequenceAltered:
		return sequenceAltered(c, to), nil
	}
	return nil, nil
}

func destructive(stmt, why string) []string {
	return []string{
		"-- DESTRUCTIVE: " + why + "; review and uncomment to apply.",
		"-- " + stmt,
	}
}

func tableAdded(c diff.Change, to *introspect.Schema) (stmts, foreignKeys []string) {
	t := to.FindTable(c.Schema, c.Object)
	if t == nil {
		return []string{"-- MISSING: table " + qualified(c.Schema, c.Object) + " not found in target model"}, nil
	}

	var lines []string
	for _, col := range t.Columns {
		lines = append(lines, "\t"+columnDDL(col))
	}
	if t.PrimaryKey != nil {
		lines = append(lines, "\tCONSTRAINT "+quoteIdent(t.PrimaryKey.Name)+" "+t.PrimaryKey.Definition)
	}
	for _, u := range t.Uniques {
		lines = append(lines, "\tCONSTRAINT "+quoteIdent(u.Name)+" "+u.Definition)
	}
	for _, ch := range t.Checks {
		lines = append(lines, "\tCONSTRAINT "+quoteIdent(ch.Name)+" "+ch.Definition)
	}

	stmt := fmt.Sprintf("CREATE TABLE %s (\n%s\n);", qualified(t.Schema, t.Name), strings.Join(lines, ",\n"))
	stmts = append(stmts, stmt)

	for _, fk := range t.ForeignKeys {
		foreignKeys = append(foreignKeys, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s;",
			qualified(t.Schema, t.Name), quoteIdent(fk.Name), fk.Definition))
	}
	for _, idx := range t.Indexes {
		stmts = append(stmts, idx.Definition+";")
	}
	return stmts, foreignKeys
}

func columnDDL(col introspect.Column) string {
	s := quoteIdent(col.Name) + " " + col.Type
	if col.Generated == "s" {
		// Reconstructing the generation expression needs the default
		// text, which holds the expression for stored columns.
		s += " GENERATED ALWAYS AS (" + col.Default + ") STORED"
		if col.NotNull {
			s += " NOT NULL"
		}
		return s
	}
	if col.NotNull {
		s += " NOT NULL"
	}
	if col.Default != "" {
		s += " DEFAULT " + col.Default
	}
	switch col.Identity {
	case "a":
		s += " GENERATED ALWAYS AS IDENTITY"
	case "d":
		s += " GENERATED BY DEFAULT AS IDENTITY"
	}
	return s
}

func columnAdded(c diff.Change, to *introspect.Schema) []string {
	t := to.FindTable(c.Schema, c.Table)
	if t == nil {
		return []string{"-- MISSING: table " + qualified(c.Schema, c.Table) + " not found in target model"}
	}
	col := t.FindColumn(c.Object)
	if col == nil {
		return []string{"-- MISSING: column " + c.Object + " not found in target model"}
	}
	return []string{fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", qualified(c.Schema, c.Table), columnDDL(*col))}
}

func columnAltered(c diff.Change, from, to *introspect.Schema) []string {
	fromTable := from.FindTable(c.Schema, c.Table)
	toTable := to.FindTable(c.Schema, c.Table)
	if fromTable == nil || toTable == nil {
		return []string{"-- MISSING: table " + qualified(c.Schema, c.Table) + " not found in both models"}
	}
	before := fromTable.FindColumn(c.Object)
	after := toTable.FindColumn(c.Object)
	if before == nil || after == nil {
		return []string{"-- MISSING: column " + c.Object + " not found in both models"}
	}

	prefix := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s", qualified(c.Schema, c.Table), quoteIdent(c.Object))
	var stmts []string

	if before.Type != after.Type {
		stmts = append(stmts,
			"-- A type change may need USING (expression) to convert existing rows.",
			fmt.Sprintf("%s TYPE %s;", prefix, after.Type))
	}
	if before.Default != after.Default {
		if after.Default == "" {
			stmts = append(stmts, prefix+" DROP DEFAULT;")
		} else {
			stmts = append(stmts, fmt.Sprintf("%s SET DEFAULT %s;", prefix, after.Default))
		}
	}
	if before.NotNull != after.NotNull {
		if after.NotNull {
			stmts = append(stmts, prefix+" SET NOT NULL;")
		} else {
			stmts = append(stmts, prefix+" DROP NOT NULL;")
		}
	}
	if before.Identity != after.Identity || before.Generated != after.Generated {
		stmts = append(stmts,
			"-- MANUAL: identity/generated change on "+qualified(c.Schema, c.Table)+"."+c.Object+";",
			"--   old: "+c.Old,
			"--   new: "+c.New)
	}
	return stmts
}

func indexChanged(c diff.Change, to *introspect.Schema) []string {
	t := to.FindTable(c.Schema, c.Table)
	if t == nil {
		return []string{"-- MISSING: table " + qualified(c.Schema, c.Table) + " not found in target model"}
	}
	var stmts []string
	if c.Kind == diff.IndexAltered {
		stmts = append(stmts, fmt.Sprintf("DROP INDEX %s;", qualified(c.Schema, c.Object)))
	}
	for _, idx := range t.Indexes {
		if idx.Name == c.Object {
			stmts = append(stmts, idx.Definition+";")
		}
	}
	return stmts
}

func constraintChanged(c diff.Change, to *introspect.Schema) (stmts, foreignKeys []string) {
	t := to.FindTable(c.Schema, c.Table)
	if t == nil {
		return []string{"-- MISSING: table " + qualified(c.Schema, c.Table) + " not found in target model"}, nil
	}

	if c.Kind == diff.ConstraintAltered {
		stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", qualified(c.Schema, c.Table), quoteIdent(c.Object)))
	}

	add := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s;", qualified(c.Schema, c.Table), quoteIdent(c.Object), c.New)
	if strings.HasPrefix(c.New, "FOREIGN KEY") {
		return stmts, []string{add}
	}
	return append(stmts, add), nil
}

func enumAdded(c diff.Change, to *introspect.Schema) []string {
	e := to.FindEnum(c.Schema, c.Object)
	if e == nil {
		return []string{"-- MISSING: enum " + qualified(c.Schema, c.Object) + " not found in target model"}
	}
	values := make([]string, len(e.Values))
	for i, v := range e.Values {
		values[i] = quoteLiteral(v)
	}
	return []string{fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);", qualified(e.Schema, e.Name), strings.Join(values, ", "))}
}

func sequenceDDL(s introspect.Sequence) string {
	cycle := "NO CYCLE"
	if s.Cycle {
		cycle = "CYCLE"
	}
	return fmt.Sprintf("START %d INCREMENT %d MINVALUE %d MAXVALUE %d %s", s.Start, s.Increment, s.Min, s.Max, cycle)
}

func sequenceAdded(c diff.Change, to *introspect.Schema) []string {
	s := to.FindSequence(c.Schema, c.Object)
	if s == nil {
		return []string{"-- MISSING: sequence " + qualified(c.Schema, c.Object) + " not found in target model"}
	}
	return []string{fmt.Sprintf("CREATE SEQUENCE %s %s;", qualified(s.Schema, s.Name), sequenceDDL(*s))}
}

func sequenceAltered(c diff.Change, to *introspect.Schema) []string {
	s := to.FindSequence(c.Schema, c.Object)
	if s == nil {
		return []string{"-- MISSING: sequence " + qualified(c.Schema, c.Object) + " not found in target model"}
	}
	return []string{fmt.Sprintf("ALTER SEQUENCE %s %s;", qualified(s.Schema, s.Name), sequenceDDL(*s))}
}

var bareIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func quoteIdent(name string) string {
	if bareIdent.MatchString(name) {
		return name
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func qualified(schemaName, name string) string {
	return quoteIdent(schemaName) + "." + quoteIdent(name)
}

func quoteLiteral(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}
