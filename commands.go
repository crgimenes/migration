package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/crgimenes/migration/core"
	"github.com/crgimenes/migration/diff"
	"github.com/crgimenes/migration/drift"
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

// printChanges groups changes under their table; enums and sequences
// (no table) print under their own qualified name. The change list is
// already in model order, so groups come out contiguous.
func printChanges(changes []diff.Change) {
	lastGroup := ""
	for _, c := range changes {
		group := c.Schema + "." + c.Table
		if c.Table == "" {
			group = c.Schema + "." + c.Object
		}
		if group != lastGroup {
			fmt.Printf("  %s\n", printHeader(group))
			lastGroup = group
		}

		bullet := printInfo("●")
		if strings.HasSuffix(string(c.Kind), "_dropped") || c.Kind == diff.EnumAltered {
			bullet = printWarning("●")
		}
		fmt.Printf("    %s %s\n", bullet, c.String())
	}
	fmt.Printf("\n  %s %s\n\n", printInfo("Σ"), printHighlight(diff.Summarize(changes)))
}

func cmdDiff(ctx context.Context, dbURL, dir string, jsonOut bool) error {
	db, _, err := openPostgres(dbURL)
	if err != nil {
		return err
	}
	defer closeDB(db)

	snapVersion, _, _, changes, err := drift.LiveDiff(ctx, db, dir)
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

	outcome, err := drift.Capture(ctx, db, config, dir, name)
	if err != nil {
		return err
	}

	if jsonOut {
		return emitJSON(struct {
			Action string `json:"action"`
			*drift.Outcome
			OK bool `json:"ok"`
		}{"capture", outcome, true})
	}

	if !outcome.Drift {
		fmt.Printf("%s %s\n", printSuccess("● No drift detected."), "Nothing to capture.")
		return nil
	}

	fmt.Printf("\n%s\n", printHeader("● Drift Captured"))
	printSeparator()
	printChanges(outcome.Changes)
	fmt.Printf("%s %s\n", printSuccess("● Migration written:"), printHighlight(outcome.UpFile))
	fmt.Printf("%s %s\n", printSuccess("● Rollback written:"), printHighlight(outcome.DownFile))
	fmt.Printf("%s %s\n", printSuccess("● Snapshot written:"), printHighlight(outcome.Snapshot))
	fmt.Printf("%s version %d registered as applied; review the generated SQL before committing.\n\n", printInfo("→"), outcome.Version)
	return nil
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
