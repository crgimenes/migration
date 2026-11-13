// Package core runs SQL file migrations against PostgreSQL and SQLite,
// tracking applied versions in the schema_migrations table.
//
// The package registers no SQL drivers: the migration binary brings
// pgx/stdlib and modernc.org/sqlite itself, and an embedding application
// (keikiban) registers whatever stack it already uses - importing core
// must not drag drivers into the consumer's binary.
package core

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type DatabaseType int

const (
	PostgreSQL DatabaseType = iota
	SQLite
)

type DatabaseConfig struct {
	Type                DatabaseType
	DriverName          string
	Placeholder         string
	CheckTableExistsSQL string
	CreateTableSQL      string
}

// scheme returns the lowercased URL scheme. The scheme is split off by
// hand instead of with url.Parse because a SQLite URL carries a
// filesystem path, and a Windows path (sqlite://C:\data\app.db) is not
// a valid URL authority - url.Parse rejects it as "invalid port".
func scheme(dbURL string) (string, bool) {
	s, _, found := strings.Cut(dbURL, ":")
	return strings.ToLower(s), found
}

// SQLiteDataSource converts a SQLite URL into the path the driver
// wants. Everything after the scheme is taken literally, so Windows
// paths survive and a relative path keeps its first segment (url.Parse
// would read that segment as a host and silently drop it).
func SQLiteDataSource(dbURL string) string {
	if dbURL == "sqlite::memory:" {
		return ":memory:"
	}
	_, rest, _ := strings.Cut(dbURL, ":")
	return strings.TrimPrefix(rest, "//")
}

func GetDatabaseConfig(dbURL string) (*DatabaseConfig, error) {
	s, found := scheme(dbURL)
	if !found {
		return nil, fmt.Errorf("missing scheme in database URL: %s", dbURL)
	}

	switch s {
	case "postgres", "postgresql":
		// "pgx" is the name pgx's database/sql adapter registers. The
		// binary (or the embedding application) provides the driver, never
		// this package.
		return &DatabaseConfig{
			Type:                PostgreSQL,
			DriverName:          "pgx",
			Placeholder:         "$1",
			CheckTableExistsSQL: `SELECT count(*) FROM information_schema.tables WHERE table_name='schema_migrations'`,
			CreateTableSQL:      `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
		}, nil
	case "sqlite":
		return &DatabaseConfig{
			Type:                SQLite,
			DriverName:          "sqlite",
			Placeholder:         "?",
			CheckTableExistsSQL: `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
			CreateTableSQL:      `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported database scheme: %s", s)
	}
}

func OpenDatabase(dbURL string, config *DatabaseConfig) (*sql.DB, error) {
	if config.Type == SQLite {
		dbURL = SQLiteDataSource(dbURL)
	}

	db, err := sql.Open(config.DriverName, dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	err = db.Ping()
	if err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("failed to ping database: %w (also failed to close: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

func CheckAndCreateMigrationsTable(ctx context.Context, db *sql.DB, config *DatabaseConfig) error {
	var count int
	err := db.QueryRowContext(ctx, config.CheckTableExistsSQL).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check migrations table: %w", err)
	}

	if count == 0 {
		_, err = db.ExecContext(ctx, config.CreateTableSQL)
		if err != nil {
			return fmt.Errorf("failed to create migrations table: %w", err)
		}
	}

	return nil
}

// GetMigrationMax returns the highest applied migration version, 0 when
// none. Creates the tracking table on first contact, like every other
// entry point.
func GetMigrationMax(ctx context.Context, db *sql.DB, config *DatabaseConfig) (int, error) {
	err := CheckAndCreateMigrationsTable(ctx, db, config)
	if err != nil {
		return 0, err
	}

	var max sql.NullInt64
	query := "SELECT MAX(version) FROM schema_migrations"
	err = db.QueryRowContext(ctx, query).Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("failed to get max migration version: %w", err)
	}

	if !max.Valid {
		return 0, nil
	}

	return int(max.Int64), nil
}

func InsertMigration(ctx context.Context, tx *sql.Tx, config *DatabaseConfig, version int) error {
	query := "INSERT INTO schema_migrations (version) VALUES (" + config.Placeholder + ")" // #nosec G202 -- only the driver placeholder ($1 or ?) is concatenated; version travels as a parameter
	_, err := tx.ExecContext(ctx, query, version)
	if err != nil {
		return fmt.Errorf("failed to insert migration version %d: %w", version, err)
	}
	return nil
}

func DeleteMigration(ctx context.Context, tx *sql.Tx, config *DatabaseConfig, version int) error {
	query := "DELETE FROM schema_migrations WHERE version = " + config.Placeholder // #nosec G202 -- only the driver placeholder ($1 or ?) is concatenated; version travels as a parameter
	_, err := tx.ExecContext(ctx, query, version)
	if err != nil {
		return fmt.Errorf("failed to delete migration version %d: %w", version, err)
	}
	return nil
}

// checkMigrationsDir refuses a directory that cannot be listed.
// migrationFiles globs it, and filepath.Glob reports NO error for
// a missing or unreadable directory - it just returns nothing. Without
// this, a typo in the directory is indistinguishable from "everything
// is up to date", and `up` reports success having applied nothing.
// An empty directory stays legitimate: a project may have no
// migrations yet.
func checkMigrationsDir(dir string) error {
	_, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("cannot read migrations directory: %w", err)
	}
	return nil
}

// migrationFiles lists the dir's files ending in suffix, ordered by
// numeric version (so 9_x sorts before 10_x even without zero padding).
// A file without a version, or two files sharing one, is refused: either
// would make the applied set ambiguous.
func migrationFiles(dir, suffix string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*"+suffix))
	if err != nil {
		return nil, err
	}
	seen := map[int]string{}
	for _, f := range files {
		v := FileVersion(f)
		if v <= 0 {
			return nil, fmt.Errorf("migration file %s has no numeric version prefix", f)
		}
		prev, dup := seen[v]
		if dup {
			return nil, fmt.Errorf("migration files %s and %s share version %d", prev, f, v)
		}
		seen[v] = f
	}
	slices.SortFunc(files, func(a, b string) int {
		return cmp.Compare(FileVersion(a), FileVersion(b))
	})
	return files, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func appliedVersions(ctx context.Context, q querier) ([]int, error) {
	rows, err := q.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("failed to read applied migrations: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var versions []int
	for rows.Next() {
		var v int
		err = rows.Scan(&v)
		if err != nil {
			return nil, fmt.Errorf("failed to read applied migrations: %w", err)
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// pendingFiles keeps the files whose version is not applied, in order.
// A file numbered below an applied version still counts as pending: it
// arrived late (merged branch) and has never run here.
func pendingFiles(ctx context.Context, q querier, dir string) ([]string, error) {
	files, err := migrationFiles(dir, ".up.sql")
	if err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, q)
	if err != nil {
		return nil, err
	}

	var pending []string
	for _, f := range files {
		_, done := slices.BinarySearch(applied, FileVersion(f))
		if !done {
			pending = append(pending, f)
		}
	}
	return pending, nil
}

// up applies the first n pending migrations, all of them when n is 0.
func up(ctx context.Context, dir string, n int, tx *sql.Tx, config *DatabaseConfig) (int, []string, error) {
	pending, err := pendingFiles(ctx, tx, dir)
	if err != nil {
		return 0, nil, err
	}
	if n > 0 && n < len(pending) {
		pending = pending[:n]
	}

	for _, f := range pending {
		err = apply(ctx, f, tx)
		if err != nil {
			return 0, nil, err
		}
		err = InsertMigration(ctx, tx, config, FileVersion(f))
		if err != nil {
			return 0, nil, err
		}
	}
	return len(pending), pending, nil
}

// down reverts the n highest applied versions, newest first, each through
// the .down.sql file of the same version; all of them when n is 0.
func down(ctx context.Context, dir string, n int, tx *sql.Tx, config *DatabaseConfig) (int, []string, error) {
	files, err := migrationFiles(dir, ".down.sql")
	if err != nil {
		return 0, nil, err
	}
	applied, err := appliedVersions(ctx, tx)
	if err != nil {
		return 0, nil, err
	}
	slices.Reverse(applied)
	if n > 0 && n < len(applied) {
		applied = applied[:n]
	}

	byVersion := map[int]string{}
	for _, f := range files {
		byVersion[FileVersion(f)] = f
	}

	executed := make([]string, 0, len(applied))
	for _, v := range applied {
		f, ok := byVersion[v]
		if !ok {
			return 0, nil, fmt.Errorf("no down file for applied version %d in %s", v, dir)
		}
		err = apply(ctx, f, tx)
		if err != nil {
			return 0, nil, err
		}
		err = DeleteMigration(ctx, tx, config, v)
		if err != nil {
			return 0, nil, err
		}
		executed = append(executed, f)
	}
	return len(executed), executed, nil
}

// FileVersion extracts the numeric version prefix of a migration file
// name; 0 when the name has no valid prefix.
func FileVersion(path string) int {
	_, file := filepath.Split(path)
	v, _, _ := strings.Cut(file, "_")
	ver, _ := strconv.Atoi(v)
	return ver
}

func apply(ctx context.Context, path string, tx *sql.Tx) error {
	content, err := os.ReadFile(path) // #nosec G304 -- path comes from the user-provided migrations dir
	if err != nil {
		return fmt.Errorf("failed to read migration file %s: %w", path, err)
	}

	_, err = tx.ExecContext(ctx, string(content))
	if err != nil {
		return fmt.Errorf("failed to execute migration %s: %w", path, err)
	}

	return nil
}

func parsePar(m []string) (int, error) {
	if len(m) == 1 {
		return 0, nil
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("failed to parse number parameter: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("migration count must not be negative: %d", n)
	}
	return n, nil
}

func status(ctx context.Context, dir string, db *sql.DB) (int, []string, error) {
	pending, err := pendingFiles(ctx, db, dir)
	if err != nil {
		return 0, nil, err
	}
	return len(pending), pending, nil
}

func Run(ctx context.Context, source, dbURL, action string) (int, []string, error) {
	config, err := GetDatabaseConfig(dbURL)
	if err != nil {
		return 0, nil, err
	}

	db, err := OpenDatabase(dbURL, config)
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		closeErr := db.Close()
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close database: %v\n", closeErr)
		}
	}()

	return RunWithExistingDatabase(ctx, source, action, db, config)
}

// RunWithExistingDatabase applies the action on an already open connection.
// All migrations in a batch commit or roll back together.
func RunWithExistingDatabase(ctx context.Context, source, action string, db *sql.DB, config *DatabaseConfig) (int, []string, error) {
	m := strings.Fields(action)
	if len(m) == 0 {
		return 0, nil, errors.New("action cannot be empty")
	}

	err := checkMigrationsDir(source)
	if err != nil {
		return 0, nil, err
	}

	err = CheckAndCreateMigrationsTable(ctx, db, config)
	if err != nil {
		return 0, nil, err
	}

	if m[0] == "status" {
		return status(ctx, source, db)
	}

	n, err := parsePar(m)
	if err != nil {
		return 0, nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to begin global transaction: %w", err)
	}
	defer func() {
		// A rollback after a successful commit is a harmless no-op.
		_ = tx.Rollback()
	}()

	var number int
	var executed []string

	switch m[0] {
	case "up":
		number, executed, err = up(ctx, source, n, tx, config)
	case "down":
		number, executed, err = down(ctx, source, n, tx, config)
	default:
		return 0, nil, fmt.Errorf("unknown action: %s", m[0])
	}

	if err != nil {
		return 0, nil, err
	}

	err = tx.Commit()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to commit global transaction: %w", err)
	}

	return number, executed, nil
}
