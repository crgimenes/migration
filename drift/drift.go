// Package drift is the orchestration other tools import: compare the
// latest schema snapshot with the live database, and capture the
// difference as a reviewable up/down migration pair. It ties together
// introspect, diff, gen, snapshot and core; the migration CLI/GUI and
// embedding applications (keikiban) share this one implementation.
//
// The caller owns the database connection. Nothing here registers SQL
// drivers, so importing this package does not drag lib/pq or the
// SQLite driver into the consumer's binary; wrap an existing *sql.DB
// with sqlx.NewDb when the connection comes from another stack (pgx
// stdlib, for example).
package drift

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/crgimenes/migration/core"
	"github.com/crgimenes/migration/diff"
	"github.com/crgimenes/migration/gen"
	"github.com/crgimenes/migration/introspect"
	"github.com/crgimenes/migration/snapshot"
	"github.com/jmoiron/sqlx"
)

// LiveDiff compares the latest snapshot in dir with the live database.
func LiveDiff(ctx context.Context, db *sqlx.DB, dir string) (snapVersion int, snap, live *introspect.Schema, changes []diff.Change, err error) {
	snapVersion, snap, found, err := snapshot.Latest(dir)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	if !found {
		return 0, nil, nil, nil, fmt.Errorf("no snapshot found in %s; record the current state first (migration snapshot)", snapshot.Dir(dir))
	}

	live, err = introspect.Read(ctx, db)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	return snapVersion, snap, live, diff.Compare(snap, live), nil
}

// Outcome is what capturing the current drift produced.
type Outcome struct {
	Drift    bool          `json:"drift"`
	Version  int           `json:"version,omitempty"`
	UpFile   string        `json:"up_file,omitempty"`
	DownFile string        `json:"down_file,omitempty"`
	Snapshot string        `json:"snapshot_file,omitempty"`
	Changes  []diff.Change `json:"changes"`
}

// Capture turns the current drift into a migration pair, registers the
// version as applied (the live database already has these changes), and
// snapshots the new state.
func Capture(ctx context.Context, db *sqlx.DB, config *core.DatabaseConfig, dir, name string) (*Outcome, error) {
	_, snap, live, changes, err := LiveDiff(ctx, db, dir)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return &Outcome{Drift: false, Changes: []diff.Change{}}, nil
	}

	current, err := core.GetMigrationMax(ctx, db, config)
	if err != nil {
		return nil, err
	}
	next := current + 1

	upSQL, downSQL := gen.Generate(changes, snap, live)
	upFile := filepath.Join(dir, fmt.Sprintf("%03d_%s.up.sql", next, name))
	downFile := filepath.Join(dir, fmt.Sprintf("%03d_%s.down.sql", next, name))
	err = writeMigrationPair(upFile, downFile, upSQL, downSQL)
	if err != nil {
		return nil, err
	}

	err = markApplied(ctx, db, config, next)
	if err != nil {
		return nil, err
	}

	snapPath, err := snapshot.Write(dir, next, live)
	if err != nil {
		return nil, err
	}

	return &Outcome{
		Drift:    true,
		Version:  next,
		UpFile:   upFile,
		DownFile: downFile,
		Snapshot: snapPath,
		Changes:  changes,
	}, nil
}

func writeMigrationPair(upFile, downFile, upSQL, downSQL string) error {
	for _, f := range []string{upFile, downFile} {
		_, err := os.Stat(f) // #nosec G703 -- paths derive from the user-provided migrations dir
		if err == nil {
			return fmt.Errorf("refusing to overwrite existing migration file %s", f)
		}
	}
	// 0644: migration files are source artifacts meant to be committed.
	err := os.WriteFile(upFile, []byte(upSQL), 0o644) // #nosec G703 G306
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", upFile, err)
	}
	err = os.WriteFile(downFile, []byte(downSQL), 0o644) // #nosec G703 G306
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", downFile, err)
	}
	return nil
}

func markApplied(ctx context.Context, db *sqlx.DB, config *core.DatabaseConfig, version int) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	err = core.InsertMigration(ctx, tx, config, version)
	if err != nil {
		return err
	}
	return tx.Commit()
}
