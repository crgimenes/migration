// Package core runs SQL file migrations against PostgreSQL and SQLite,
// tracking applied versions in the schema_migrations table.
package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
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
		return &DatabaseConfig{
			Type:                PostgreSQL,
			DriverName:          "postgres",
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

func OpenDatabase(dbURL string, config *DatabaseConfig) (*sqlx.DB, error) {
	if config.Type == SQLite {
		dbURL = SQLiteDataSource(dbURL)
	}

	db, err := sqlx.Open(config.DriverName, dbURL)
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

func CheckAndCreateMigrationsTable(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) error {
	var count int
	err := db.GetContext(ctx, &count, config.CheckTableExistsSQL)
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

func GetMigrationCount(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) (int, error) {
	err := CheckAndCreateMigrationsTable(ctx, db, config)
	if err != nil {
		return 0, err
	}

	var count int
	query := "SELECT COUNT(*) FROM schema_migrations"
	err = db.GetContext(ctx, &count, query)
	if err != nil {
		return 0, fmt.Errorf("failed to get migration count: %w", err)
	}

	return count, nil
}

// GetMigrationMax returns the highest applied migration version, 0 when
// none. Creates the tracking table on first contact, like every other
// entry point.
func GetMigrationMax(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) (int, error) {
	err := CheckAndCreateMigrationsTable(ctx, db, config)
	if err != nil {
		return 0, err
	}

	var max sql.NullInt64
	query := "SELECT MAX(version) FROM schema_migrations"
	err = db.GetContext(ctx, &max, query)
	if err != nil {
		return 0, fmt.Errorf("failed to get max migration version: %w", err)
	}

	if !max.Valid {
		return 0, nil
	}

	return int(max.Int64), nil
}

func GetMigrationMaxTx(ctx context.Context, tx *sqlx.Tx) (int, error) {
	var max sql.NullInt64
	query := "SELECT MAX(version) FROM schema_migrations"
	err := tx.GetContext(ctx, &max, query)
	if err != nil {
		return 0, fmt.Errorf("failed to get max migration version: %w", err)
	}

	if !max.Valid {
		return 0, nil
	}

	return int(max.Int64), nil
}

func InsertMigration(ctx context.Context, tx *sqlx.Tx, config *DatabaseConfig, version int) error {
	query := "INSERT INTO schema_migrations (version) VALUES (" + config.Placeholder + ")"
	_, err := tx.ExecContext(ctx, query, version)
	if err != nil {
		return fmt.Errorf("failed to insert migration version %d: %w", version, err)
	}
	return nil
}

func DeleteMigration(ctx context.Context, tx *sqlx.Tx, config *DatabaseConfig, version int) error {
	query := "DELETE FROM schema_migrations WHERE version = " + config.Placeholder
	_, err := tx.ExecContext(ctx, query, version)
	if err != nil {
		return fmt.Errorf("failed to delete migration version %d: %w", version, err)
	}
	return nil
}

func upFiles(dir string) (files []string, err error) {
	files, err = filepath.Glob(filepath.Join(dir, "*.up.sql"))
	return
}

// downFiles returns down files newest-first. n is the number of applied
// migrations: the slice drops down files newer than the applied set, so
// execDown reverts only what the database actually has.
func downFiles(dir string, n int) (files []string, err error) {
	files, err = filepath.Glob(filepath.Join(dir, "*.down.sql"))
	if err != nil {
		return nil, err
	}
	if n > len(files) {
		return nil, fmt.Errorf("%d migrations applied but only %d down files in %s", n, len(files), dir)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	files = files[len(files)-n:]
	return
}

func up(ctx context.Context, source string, start, n int, tx *sqlx.Tx, config *DatabaseConfig) (number int, executed []string, err error) {
	files, err := upFiles(source)
	if err != nil {
		return
	}
	number, executed, err = execUp(ctx, files, start, n, tx, config)
	return
}

func down(ctx context.Context, source string, start, n int, tx *sqlx.Tx, config *DatabaseConfig) (number int, executed []string, err error) {
	nfiles, err := GetMigrationMaxTx(ctx, tx)
	if err != nil {
		return
	}
	if n == 0 {
		n = nfiles
	}
	files, err := downFiles(source, nfiles)
	if err != nil {
		return
	}
	number, executed, err = execDown(ctx, files, start, n, tx, config)
	return
}

func execUp(ctx context.Context, files []string, start, n int, tx *sqlx.Tx, config *DatabaseConfig) (number int, executed []string, err error) {
	if n == 0 {
		n = len(files) - start
	}
	for i := start; i < len(files) && i < start+n; i++ {
		v := version(files[i])
		if err = apply(ctx, files[i], tx); err != nil {
			return
		}
		if err = InsertMigration(ctx, tx, config, v); err != nil {
			return
		}
		executed = append(executed, files[i])
		number++
	}
	return
}

func execDown(ctx context.Context, files []string, start, n int, tx *sqlx.Tx, config *DatabaseConfig) (number int, executed []string, err error) {
	if n == 0 {
		n = len(files) - start
	}
	for i := start; i < len(files) && i < start+n; i++ {
		v := version(files[i])
		if err = apply(ctx, files[i], tx); err != nil {
			return
		}
		if err = DeleteMigration(ctx, tx, config, v); err != nil {
			return
		}
		executed = append(executed, files[i])
		number++
	}
	return
}

// FileVersion extracts the numeric version prefix of a migration file
// name; 0 when the name has no valid prefix.
func FileVersion(path string) int {
	return version(path)
}

func version(path string) int {
	_, file := filepath.Split(path)
	v, _, _ := strings.Cut(file, "_")
	ver, _ := strconv.Atoi(v)
	return ver
}

func apply(ctx context.Context, path string, tx *sqlx.Tx) error {
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
	return n, nil
}

func status(ctx context.Context, source string, db *sqlx.DB, config *DatabaseConfig) (int, []string, error) {
	executed, err := GetMigrationCount(ctx, db, config)
	if err != nil {
		return 0, nil, err
	}
	up, err := upFiles(source)
	if err != nil {
		return 0, nil, err
	}
	diff := len(up) - executed
	if diff == 0 {
		return 0, nil, nil
	}
	if diff < 0 {
		diff = -1 * diff
	}
	return diff, up[len(up)-diff:], nil
}

func doDown(ctx context.Context, m []string, source string, tx *sqlx.Tx, config *DatabaseConfig) (number int, executed []string, err error) {
	n, err := parsePar(m)
	if err != nil {
		return
	}
	number, executed, err = down(ctx, source, 0, n, tx, config)
	return
}

func doUp(ctx context.Context, m []string, source string, tx *sqlx.Tx, config *DatabaseConfig) (number int, executed []string, err error) {
	n, err := parsePar(m)
	if err != nil {
		return
	}
	start, err := GetMigrationMaxTx(ctx, tx)
	if err != nil {
		return
	}
	number, executed, err = up(ctx, source, start, n, tx, config)
	return
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
func RunWithExistingDatabase(ctx context.Context, source, action string, db *sqlx.DB, config *DatabaseConfig) (int, []string, error) {
	err := CheckAndCreateMigrationsTable(ctx, db, config)
	if err != nil {
		return 0, nil, err
	}

	m := strings.Fields(action)
	if len(m) == 0 {
		return 0, nil, errors.New("action cannot be empty")
	}

	if m[0] == "status" {
		return status(ctx, source, db, config)
	}

	tx, err := db.BeginTxx(ctx, nil)
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
		number, executed, err = doUp(ctx, m, source, tx, config)
	case "down":
		number, executed, err = doDown(ctx, m, source, tx, config)
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
