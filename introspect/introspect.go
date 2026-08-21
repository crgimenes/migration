// Package introspect reads the structure of a live PostgreSQL database
// into a comparable, JSON-serializable model. Constraint and index
// definitions are kept as the canonical DDL text produced by
// pg_get_constraintdef/pg_get_indexdef, so comparing two models is
// string comparison and the definitions can be replayed as SQL.
package introspect

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// FormatVersion is bumped when the model shape changes; Load refuses
// snapshots written by a newer format.
const FormatVersion = 1

type Schema struct {
	Format    int        `json:"format"`
	Tables    []Table    `json:"tables"`
	Enums     []Enum     `json:"enums"`
	Sequences []Sequence `json:"sequences"`
}

type Table struct {
	Schema      string       `json:"schema"`
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	PrimaryKey  *Constraint  `json:"primary_key,omitempty"`
	ForeignKeys []Constraint `json:"foreign_keys,omitempty"`
	Uniques     []Constraint `json:"uniques,omitempty"`
	Checks      []Constraint `json:"checks,omitempty"`
	Indexes     []Index      `json:"indexes,omitempty"`
}

type Column struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	NotNull   bool   `json:"not_null"`
	Default   string `json:"default,omitempty"`
	Identity  string `json:"identity,omitempty"`  // "a" always, "d" by default
	Generated string `json:"generated,omitempty"` // "s" stored
}

type Constraint struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

type Index struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

type Enum struct {
	Schema string   `json:"schema"`
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type Sequence struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Start     int64  `json:"start"`
	Increment int64  `json:"increment"`
	Min       int64  `json:"min"`
	Max       int64  `json:"max"`
	Cycle     bool   `json:"cycle"`
}

// excludedSchemas keeps PostgreSQL's own objects out of the model.
// schema_migrations is this tool's bookkeeping table, not user schema.
const excludedSchemas = `('pg_catalog', 'information_schema', 'pg_toast')`

// Read introspects the connected database. Every query orders its
// results, so two reads of the same schema produce identical models.
func Read(ctx context.Context, db *sqlx.DB) (*Schema, error) {
	schema := &Schema{Format: FormatVersion}

	tables, index, err := readTables(ctx, db)
	if err != nil {
		return nil, err
	}

	err = readColumns(ctx, db, index)
	if err != nil {
		return nil, err
	}

	err = readConstraints(ctx, db, index)
	if err != nil {
		return nil, err
	}

	err = readIndexes(ctx, db, index)
	if err != nil {
		return nil, err
	}

	schema.Tables = make([]Table, len(tables))
	for i, key := range tables {
		schema.Tables[i] = *index[key]
	}

	schema.Enums, err = readEnums(ctx, db)
	if err != nil {
		return nil, err
	}

	schema.Sequences, err = readSequences(ctx, db)
	if err != nil {
		return nil, err
	}

	return schema, nil
}

func tableKey(schema, name string) string {
	return schema + "." + name
}

func readTables(ctx context.Context, db *sqlx.DB) ([]string, map[string]*Table, error) {
	const query = `SELECT
			n.nspname, -- 1
			c.relname  -- 2
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p')
		AND NOT c.relispartition
		AND n.nspname NOT IN ` + excludedSchemas + `
		AND c.relname <> 'schema_migrations'
		ORDER BY n.nspname, c.relname`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var keys []string
	index := map[string]*Table{}
	for rows.Next() {
		t := Table{}
		err = rows.Scan(
			&t.Schema, // 1
			&t.Name,   // 2
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to scan table: %w", err)
		}
		key := tableKey(t.Schema, t.Name)
		keys = append(keys, key)
		index[key] = &t
	}
	return keys, index, rows.Err()
}

func readColumns(ctx context.Context, db *sqlx.DB, index map[string]*Table) error {
	const query = `SELECT
			n.nspname,                                    -- 1
			c.relname,                                    -- 2
			a.attname,                                    -- 3
			format_type(a.atttypid, a.atttypmod),         -- 4
			a.attnotnull,                                 -- 5
			COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), -- 6
			a.attidentity,                                -- 7
			a.attgenerated                                -- 8
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE c.relkind IN ('r', 'p')
		AND NOT c.relispartition
		AND n.nspname NOT IN ` + excludedSchemas + `
		AND c.relname <> 'schema_migrations'
		AND a.attnum > 0
		AND NOT a.attisdropped
		ORDER BY n.nspname, c.relname, a.attnum`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to list columns: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var schemaName, tableName string
		col := Column{}
		err = rows.Scan(
			&schemaName,    // 1
			&tableName,     // 2
			&col.Name,      // 3
			&col.Type,      // 4
			&col.NotNull,   // 5
			&col.Default,   // 6
			&col.Identity,  // 7
			&col.Generated, // 8
		)
		if err != nil {
			return fmt.Errorf("failed to scan column: %w", err)
		}
		t := index[tableKey(schemaName, tableName)]
		if t == nil {
			continue
		}
		t.Columns = append(t.Columns, col)
	}
	return rows.Err()
}

func readConstraints(ctx context.Context, db *sqlx.DB, index map[string]*Table) error {
	const query = `SELECT
			n.nspname,                      -- 1
			c.relname,                      -- 2
			con.conname,                    -- 3
			con.contype,                    -- 4
			pg_get_constraintdef(con.oid)   -- 5
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname NOT IN ` + excludedSchemas + `
		AND c.relname <> 'schema_migrations'
		AND con.contype IN ('p', 'f', 'u', 'c')
		ORDER BY n.nspname, c.relname, con.conname`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to list constraints: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var schemaName, tableName, conType string
		con := Constraint{}
		err = rows.Scan(
			&schemaName,     // 1
			&tableName,      // 2
			&con.Name,       // 3
			&conType,        // 4
			&con.Definition, // 5
		)
		if err != nil {
			return fmt.Errorf("failed to scan constraint: %w", err)
		}
		t := index[tableKey(schemaName, tableName)]
		if t == nil {
			continue
		}
		switch conType {
		case "p":
			t.PrimaryKey = &con
		case "f":
			t.ForeignKeys = append(t.ForeignKeys, con)
		case "u":
			t.Uniques = append(t.Uniques, con)
		case "c":
			t.Checks = append(t.Checks, con)
		}
	}
	return rows.Err()
}

func readIndexes(ctx context.Context, db *sqlx.DB, index map[string]*Table) error {
	// Indexes backing PK/unique constraints are excluded: the
	// constraint entry already carries them, and listing both would
	// make every such index show up twice in a diff.
	const query = `SELECT
			n.nspname,                  -- 1
			t.relname,                  -- 2
			i.relname,                  -- 3
			pg_get_indexdef(i.oid)      -- 4
		FROM pg_index x
		JOIN pg_class i ON i.oid = x.indexrelid
		JOIN pg_class t ON t.oid = x.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname NOT IN ` + excludedSchemas + `
		AND t.relname <> 'schema_migrations'
		AND NOT EXISTS (
			SELECT 1 FROM pg_constraint con WHERE con.conindid = x.indexrelid
		)
		ORDER BY n.nspname, t.relname, i.relname`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to list indexes: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var schemaName, tableName string
		idx := Index{}
		err = rows.Scan(
			&schemaName,     // 1
			&tableName,      // 2
			&idx.Name,       // 3
			&idx.Definition, // 4
		)
		if err != nil {
			return fmt.Errorf("failed to scan index: %w", err)
		}
		t := index[tableKey(schemaName, tableName)]
		if t == nil {
			continue
		}
		t.Indexes = append(t.Indexes, idx)
	}
	return rows.Err()
}

func readEnums(ctx context.Context, db *sqlx.DB) ([]Enum, error) {
	const query = `SELECT
			n.nspname,   -- 1
			t.typname,   -- 2
			e.enumlabel  -- 3
		FROM pg_enum e
		JOIN pg_type t ON t.oid = e.enumtypid
		JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname NOT IN ` + excludedSchemas + `
		ORDER BY n.nspname, t.typname, e.enumsortorder`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list enums: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var enums []Enum
	for rows.Next() {
		var schemaName, enumName, label string
		err = rows.Scan(
			&schemaName, // 1
			&enumName,   // 2
			&label,      // 3
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan enum: %w", err)
		}
		if len(enums) == 0 || enums[len(enums)-1].Schema != schemaName || enums[len(enums)-1].Name != enumName {
			enums = append(enums, Enum{Schema: schemaName, Name: enumName})
		}
		enums[len(enums)-1].Values = append(enums[len(enums)-1].Values, label)
	}
	return enums, rows.Err()
}

func readSequences(ctx context.Context, db *sqlx.DB) ([]Sequence, error) {
	// Sequences owned by a column (serial, identity) follow their
	// column and are excluded; only standalone sequences are schema
	// objects in their own right.
	const query = `SELECT
			n.nspname,      -- 1
			c.relname,      -- 2
			s.seqstart,     -- 3
			s.seqincrement, -- 4
			s.seqmin,       -- 5
			s.seqmax,       -- 6
			s.seqcycle      -- 7
		FROM pg_sequence s
		JOIN pg_class c ON c.oid = s.seqrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname NOT IN ` + excludedSchemas + `
		AND NOT EXISTS (
			SELECT 1 FROM pg_depend d
			WHERE d.objid = c.oid AND d.deptype IN ('a', 'i')
		)
		ORDER BY n.nspname, c.relname`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list sequences: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var sequences []Sequence
	for rows.Next() {
		seq := Sequence{}
		err = rows.Scan(
			&seq.Schema,    // 1
			&seq.Name,      // 2
			&seq.Start,     // 3
			&seq.Increment, // 4
			&seq.Min,       // 5
			&seq.Max,       // 6
			&seq.Cycle,     // 7
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan sequence: %w", err)
		}
		sequences = append(sequences, seq)
	}
	return sequences, rows.Err()
}

// FindTable returns the table or nil.
func (s *Schema) FindTable(schemaName, name string) *Table {
	for i := range s.Tables {
		if s.Tables[i].Schema == schemaName && s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	return nil
}

// FindEnum returns the enum or nil.
func (s *Schema) FindEnum(schemaName, name string) *Enum {
	for i := range s.Enums {
		if s.Enums[i].Schema == schemaName && s.Enums[i].Name == name {
			return &s.Enums[i]
		}
	}
	return nil
}

// FindSequence returns the sequence or nil.
func (s *Schema) FindSequence(schemaName, name string) *Sequence {
	for i := range s.Sequences {
		if s.Sequences[i].Schema == schemaName && s.Sequences[i].Name == name {
			return &s.Sequences[i]
		}
	}
	return nil
}

// FindColumn returns the column or nil.
func (t *Table) FindColumn(name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

// ToJSON serializes the model for snapshot files. json/v2 keeps DDL
// text readable (no HTML escaping of < > & inside check definitions);
// FormatNilSliceAsNull preserves the nil/empty distinction so a
// serialize/parse round trip returns a DeepEqual model.
func (s *Schema) ToJSON() ([]byte, error) {
	return json.Marshal(s, jsontext.WithIndent("\t"), json.FormatNilSliceAsNull(true))
}

// Load parses a snapshot and validates its format version. Snapshot
// files are hand-editable, so the stricter json/v2 defaults (rejecting
// duplicate object names and invalid UTF-8) are the point.
func Load(data []byte) (*Schema, error) {
	s := &Schema{}
	err := json.Unmarshal(data, s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse snapshot: %w", err)
	}
	if s.Format > FormatVersion {
		return nil, fmt.Errorf("snapshot format %d is newer than supported format %d", s.Format, FormatVersion)
	}
	return s, nil
}
