package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/crgimenes/migration/core"
	"github.com/crgimenes/migration/diff"
	"github.com/crgimenes/migration/gen"
	"github.com/crgimenes/migration/introspect"
	"github.com/crgimenes/migration/snapshot"
	"github.com/jmoiron/sqlx"
)

// errDrift makes `migration diff` exit with code 2 in CI: 0 = clean,
// 1 = failure, 2 = drift found.
var errDrift = errors.New("schema drift detected")

func openPostgres(dbURL string) (*sqlx.DB, *core.DatabaseConfig, error) {
	config, err := core.GetDatabaseConfig(dbURL)
	if err != nil {
		return nil, nil, err
	}
	if config.Type != core.PostgreSQL {
		return nil, nil, errors.New("schema introspection commands require a PostgreSQL URL")
	}
	db, err := core.OpenDatabase(dbURL, config)
	if err != nil {
		return nil, nil, err
	}
	return db, config, nil
}

func closeDB(db *sqlx.DB) {
	err := db.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed to close database: %v\n", printWarning("● Warning:"), err)
	}
}

func emitJSON(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}

func cmdSnapshot(ctx context.Context, dbURL, dir string, jsonOut bool) error {
	db, config, err := openPostgres(dbURL)
	if err != nil {
		return err
	}
	defer closeDB(db)

	v, err := core.GetMigrationMax(ctx, db, config)
	if err != nil {
		return err
	}
	s, err := introspect.Read(ctx, db)
	if err != nil {
		return err
	}
	path, err := snapshot.Write(dir, v, s)
	if err != nil {
		return err
	}

	if jsonOut {
		return emitJSON(struct {
			Action  string `json:"action"`
			Version int    `json:"version"`
			Path    string `json:"path"`
			OK      bool   `json:"ok"`
		}{"snapshot", v, path, true})
	}
	fmt.Printf("%s %s %s\n", printSuccess("● Snapshot written:"), printHighlight(path), printInfo(fmt.Sprintf("(version %d)", v)))
	return nil
}

// liveDiff compares the latest snapshot with the live database.
func liveDiff(ctx context.Context, db *sqlx.DB, dir string) (snapVersion int, snap, live *introspect.Schema, changes []diff.Change, err error) {
	snapVersion, snap, found, err := snapshot.Latest(dir)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	if !found {
		return 0, nil, nil, nil, fmt.Errorf("no snapshot found in %s; run `%s snapshot` first to record the current state", snapshot.Dir(dir), os.Args[0])
	}

	live, err = introspect.Read(ctx, db)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	return snapVersion, snap, live, diff.Compare(snap, live), nil
}

func printChanges(changes []diff.Change) {
	for _, c := range changes {
		bullet := printInfo("●")
		if strings.HasSuffix(string(c.Kind), "_dropped") || c.Kind == diff.EnumAltered {
			bullet = printWarning("●")
		}
		fmt.Printf("  %s %s\n", bullet, c.String())
	}
	fmt.Println()
}

func cmdDiff(ctx context.Context, dbURL, dir string, jsonOut bool) error {
	db, _, err := openPostgres(dbURL)
	if err != nil {
		return err
	}
	defer closeDB(db)

	snapVersion, _, _, changes, err := liveDiff(ctx, db, dir)
	if err != nil {
		return err
	}

	if jsonOut {
		encErr := emitJSON(struct {
			Action   string        `json:"action"`
			Snapshot int           `json:"snapshot"`
			Drift    bool          `json:"drift"`
			Changes  []diff.Change `json:"changes"`
			OK       bool          `json:"ok"`
		}{"diff", snapVersion, len(changes) > 0, nonNil(changes), true})
		if encErr != nil {
			return encErr
		}
		if len(changes) > 0 {
			return errDrift
		}
		return nil
	}

	fmt.Printf("\n%s\n", printHeader("● Schema Drift"))
	printSeparator()
	fmt.Printf("%s %s\n", printInfo("→ Comparing live database against snapshot:"), printHighlight(fmt.Sprintf("%d", snapVersion)))
	if len(changes) == 0 {
		fmt.Printf("%s %s\n\n", printSuccess("● No drift detected."), "The live schema matches the snapshot.")
		return nil
	}
	fmt.Printf("%s %s %s\n", printWarning("● Drift detected:"), printHighlight(fmt.Sprintf("%d", len(changes))), "changes")
	printSeparator()
	printChanges(changes)
	fmt.Printf("%s %s\n\n", printInfo("→ Turn this into a migration with:"), printHighlight(os.Args[0]+" capture <name>"))
	return errDrift
}

func cmdCapture(ctx context.Context, dbURL, dir, name string, jsonOut bool) error {
	db, config, err := openPostgres(dbURL)
	if err != nil {
		return err
	}
	defer closeDB(db)

	_, snap, live, changes, err := liveDiff(ctx, db, dir)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		if jsonOut {
			return emitJSON(struct {
				Action string `json:"action"`
				Drift  bool   `json:"drift"`
				OK     bool   `json:"ok"`
			}{"capture", false, true})
		}
		fmt.Printf("%s %s\n", printSuccess("● No drift detected."), "Nothing to capture.")
		return nil
	}

	current, err := core.GetMigrationMax(ctx, db, config)
	if err != nil {
		return err
	}
	next := current + 1

	upSQL, downSQL := gen.Generate(changes, snap, live)
	upFile := filepath.Join(dir, fmt.Sprintf("%03d_%s.up.sql", next, name))
	downFile := filepath.Join(dir, fmt.Sprintf("%03d_%s.down.sql", next, name))
	err = writeMigrationPair(upFile, downFile, upSQL, downSQL)
	if err != nil {
		return err
	}

	// The captured changes are already applied in the live database, so
	// the new version is registered as executed instead of pending.
	err = markApplied(ctx, db, config, next)
	if err != nil {
		return err
	}

	snapPath, err := snapshot.Write(dir, next, live)
	if err != nil {
		return err
	}

	if jsonOut {
		return emitJSON(struct {
			Action   string        `json:"action"`
			Drift    bool          `json:"drift"`
			Version  int           `json:"version"`
			UpFile   string        `json:"up_file"`
			DownFile string        `json:"down_file"`
			Snapshot string        `json:"snapshot_file"`
			Changes  []diff.Change `json:"changes"`
			OK       bool          `json:"ok"`
		}{"capture", true, next, upFile, downFile, snapPath, changes, true})
	}

	fmt.Printf("\n%s\n", printHeader("● Drift Captured"))
	printSeparator()
	printChanges(changes)
	fmt.Printf("%s %s\n", printSuccess("● Migration written:"), printHighlight(upFile))
	fmt.Printf("%s %s\n", printSuccess("● Rollback written:"), printHighlight(downFile))
	fmt.Printf("%s %s\n", printSuccess("● Snapshot written:"), printHighlight(snapPath))
	fmt.Printf("%s version %d registered as applied; review the generated SQL before committing.\n\n", printInfo("→"), next)
	return nil
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

// resolvePoint loads one side of a report: an integer means a snapshot
// version in dir, "live" means the -url database, and a URL is opened
// directly.
func resolvePoint(ctx context.Context, dir, dbURL, arg string) (*introspect.Schema, error) {
	v, err := strconv.Atoi(arg)
	if err == nil {
		return snapshot.Load(dir, v)
	}

	target := arg
	if arg == "live" {
		if dbURL == "" {
			return nil, errors.New("`report ... live` needs -url (or DATABASE_URL)")
		}
		target = dbURL
	}

	db, _, err := openPostgres(target)
	if err != nil {
		return nil, err
	}
	defer closeDB(db)
	return introspect.Read(ctx, db)
}

func cmdReport(ctx context.Context, dbURL, dir string, args []string, jsonOut bool) error {
	if len(args) != 2 {
		return errors.New("usage: report <from> <to> (snapshot version, `live`, or a database URL)")
	}

	from, err := resolvePoint(ctx, dir, dbURL, args[0])
	if err != nil {
		return err
	}
	to, err := resolvePoint(ctx, dir, dbURL, args[1])
	if err != nil {
		return err
	}

	changes := diff.Compare(from, to)

	if jsonOut {
		return emitJSON(struct {
			Action  string        `json:"action"`
			From    string        `json:"from"`
			To      string        `json:"to"`
			Changes []diff.Change `json:"changes"`
			OK      bool          `json:"ok"`
		}{"report", args[0], args[1], nonNil(changes), true})
	}

	fmt.Printf("\n%s\n", printHeader("● Schema Report"))
	printSeparator()
	fmt.Printf("%s %s %s %s\n", printInfo("→ From"), printHighlight(args[0]), printInfo("to"), printHighlight(args[1]))
	if len(changes) == 0 {
		fmt.Printf("%s %s\n\n", printSuccess("● No differences."), "The two schemas are identical.")
		return nil
	}
	fmt.Printf("%s %s %s\n", printInfo("● Differences:"), printHighlight(fmt.Sprintf("%d", len(changes))), "changes")
	printSeparator()
	printChanges(changes)
	return nil
}

func nonNil(changes []diff.Change) []diff.Change {
	if changes == nil {
		return []diff.Change{}
	}
	return changes
}
