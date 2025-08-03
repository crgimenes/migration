package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
	"golang.org/x/xerrors"
)

var (
	// Version of migration app
	Version string
)

// DatabaseType represents the type of database
type DatabaseType int

const (
	PostgreSQL DatabaseType = iota
	SQLite
)

// DatabaseConfig holds database-specific configuration
type DatabaseConfig struct {
	Type                DatabaseType
	DriverName          string
	Placeholder         string
	CheckTableExistsSQL string
	CreateTableSQL      string
}

// GetDatabaseConfig returns the appropriate config based on URL scheme
func GetDatabaseConfig(dbURL string) (*DatabaseConfig, error) {
	// Handle special case for SQLite memory database
	if dbURL == "sqlite::memory:" {
		return &DatabaseConfig{
			Type:                SQLite,
			DriverName:          "sqlite",
			Placeholder:         "?",
			CheckTableExistsSQL: `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
			CreateTableSQL:      `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
		}, nil
	}

	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse database URL: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
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
		return nil, xerrors.Errorf("unsupported database scheme: %s", u.Scheme)
	}
}

// OpenDatabase opens a database connection with the appropriate driver
func OpenDatabase(dbURL string, config *DatabaseConfig) (*sqlx.DB, error) {
	// For SQLite, extract the path from the URL
	if config.Type == SQLite && dbURL != "sqlite::memory:" {
		u, err := url.Parse(dbURL)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse SQLite URL: %w", err)
		}
		dbURL = u.Path
	}

	db, err := sqlx.Open(config.DriverName, dbURL)
	if err != nil {
		return nil, xerrors.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, xerrors.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// CheckAndCreateMigrationsTable ensures the migrations table exists
func CheckAndCreateMigrationsTable(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) error {
	var count int
	err := db.GetContext(ctx, &count, config.CheckTableExistsSQL)
	if err != nil {
		return xerrors.Errorf("failed to check migrations table: %w", err)
	}

	if count == 0 {
		_, err = db.ExecContext(ctx, config.CreateTableSQL)
		if err != nil {
			return xerrors.Errorf("failed to create migrations table: %w", err)
		}
	}

	return nil
}

// GetMigrationMax returns the maximum migration version in the database
func GetMigrationMax(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) (int, error) {
	err := CheckAndCreateMigrationsTable(ctx, db, config)
	if err != nil {
		return 0, err
	}

	var max sql.NullInt64
	query := "SELECT MAX(version) FROM schema_migrations"
	err = db.GetContext(ctx, &max, query)
	if err != nil {
		return 0, xerrors.Errorf("failed to get max migration version: %w", err)
	}

	if !max.Valid {
		return 0, nil
	}

	return int(max.Int64), nil
}

// InsertMigration inserts a migration version into the database
func InsertMigration(ctx context.Context, db *sqlx.DB, config *DatabaseConfig, version int) error {
	query := "INSERT INTO schema_migrations (version) VALUES (" + config.Placeholder + ")"
	_, err := db.ExecContext(ctx, query, version)
	if err != nil {
		return xerrors.Errorf("failed to insert migration version %d: %w", version, err)
	}
	return nil
}

// DeleteMigration deletes a migration version from the database
func DeleteMigration(ctx context.Context, db *sqlx.DB, config *DatabaseConfig, version int) error {
	query := "DELETE FROM schema_migrations WHERE version = " + config.Placeholder
	_, err := db.ExecContext(ctx, query, version)
	if err != nil {
		return xerrors.Errorf("failed to delete migration version %d: %w", version, err)
	}
	return nil
}

// upFiles search for migration up files and return
// a sorted array with the path of all found files
func upFiles(dir string) (files []string, err error) {
	files, err = filepath.Glob(filepath.Join(dir, "*.up.sql"))
	return
}

// downFiles search for migration down files and return
// a sorted array with the path of all found files
func downFiles(dir string, n int) (files []string, err error) {
	files, err = filepath.Glob(filepath.Join(dir, "*.down.sql"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	files = files[len(files)-n:]
	return
}

func up(ctx context.Context, source string, start, n int, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	files, err := upFiles(source)
	if err != nil {
		return
	}
	number, executed, err = execUp(ctx, files, start, n, db, config)
	return
}

func down(ctx context.Context, source string, start, n int, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	nfiles, err := GetMigrationMax(ctx, db, config)
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
	number, executed, err = execDown(ctx, files, start, n, db, config)
	return
}

func execUp(ctx context.Context, files []string, start, n int, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	if n == 0 {
		n = len(files) - start
	}
	for i := start; i < len(files) && i < start+n; i++ {
		v := version(files[i])
		if err = apply(ctx, files[i], db); err != nil {
			return
		}
		if err = InsertMigration(ctx, db, config, v); err != nil {
			return
		}
		executed = append(executed, files[i])
		number++
	}
	return
}

func execDown(ctx context.Context, files []string, start, n int, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	if n == 0 {
		n = len(files) - start
	}
	for i := start; i < len(files) && i < start+n; i++ {
		v := version(files[i])
		if err = apply(ctx, files[i], db); err != nil {
			return
		}
		if err = DeleteMigration(ctx, db, config, v); err != nil {
			return
		}
		executed = append(executed, files[i])
		number++
	}
	return
}

func version(path string) int {
	_, file := filepath.Split(path)
	v := strings.Split(file, "_")[0]
	ver, _ := strconv.Atoi(v)
	return ver
}

func apply(ctx context.Context, path string, db *sqlx.DB) error {
	file, err := os.Open(path)
	if err != nil {
		return xerrors.Errorf("failed to open migration file %s: %w", path, err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return xerrors.Errorf("failed to read migration file %s: %w", path, err)
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, string(content))
	if err != nil {
		return xerrors.Errorf("failed to execute migration %s: %w", path, err)
	}

	if err = tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit migration %s: %w", path, err)
	}

	return nil
}

func parsePar(m []string) (int, error) {
	if len(m) == 1 {
		return 0, nil
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, xerrors.Errorf("failed to parse number parameter: %w", err)
	}
	return n, nil
}

// Status checks database status
func Status(ctx context.Context, source string, db *sqlx.DB, config *DatabaseConfig) (int, []string, error) {
	n, err := GetMigrationMax(ctx, db, config)
	if err != nil {
		return 0, nil, err
	}
	up, err := upFiles(source)
	if err != nil {
		return 0, nil, err
	}
	diff := len(up) - n
	if diff == 0 {
		return 0, nil, nil
	}
	if diff < 0 {
		diff = -1 * diff
	}
	return diff, up[len(up)-diff:], nil
}

func doDown(ctx context.Context, m []string, source string, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	n, err := parsePar(m)
	if err != nil {
		return
	}
	number, executed, err = down(ctx, source, 0, n, db, config)
	return
}

func doUp(ctx context.Context, m []string, source string, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	n, err := parsePar(m)
	if err != nil {
		return
	}
	start, err := GetMigrationMax(ctx, db, config)
	if err != nil {
		return
	}
	number, executed, err = up(ctx, source, start, n, db, config)
	return
}

// Run executes migrations with the given action
func Run(ctx context.Context, source, dbURL, action string) (int, []string, error) {
	return RunWithDatabase(ctx, source, dbURL, action)
}

// RunWithDatabase executes migrations with the given action using database abstraction
func RunWithDatabase(ctx context.Context, source, dbURL, action string) (int, []string, error) {
	config, err := GetDatabaseConfig(dbURL)
	if err != nil {
		return 0, nil, err
	}

	db, err := OpenDatabase(dbURL, config)
	if err != nil {
		return 0, nil, err
	}
	defer db.Close()

	m := strings.Fields(action)
	if len(m) == 0 {
		return 0, nil, xerrors.New("action cannot be empty")
	}

	switch m[0] {
	case "up":
		return doUp(ctx, m, source, db, config)
	case "down":
		return doDown(ctx, m, source, db, config)
	case "status":
		return Status(ctx, source, db, config)
	default:
		return 0, nil, xerrors.Errorf("unknown action: %s", m[0])
	}
}

// Execute starts the migration app CLI
func Execute() error {
	var (
		dbURL   = flag.String("url", os.Getenv("DATABASE_URL"), "DB URL")
		dir     = flag.String("dir", os.Getenv("MIGRATIONS"), "Migrations dir")
		action  = flag.String("action", os.Getenv("ACTION"), "Migrations action")
		version = flag.Bool("version", false, "Show version")
		help    = flag.Bool("help", false, "Show help")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Migration Tool\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *version {
		fmt.Printf("Migration tool version=%s\n", Version)
		return nil
	}

	if *help {
		flag.Usage()
		return nil
	}

	if *dbURL == "" {
		fmt.Fprintf(os.Stderr, "Error: database URL is required\n")
		flag.Usage()
		return fmt.Errorf("database URL is required")
	}

	if *dir == "" {
		fmt.Fprintf(os.Stderr, "Error: migrations directory is required\n")
		flag.Usage()
		return fmt.Errorf("migrations directory is required")
	}

	if *action == "" {
		fmt.Fprintf(os.Stderr, "Error: action is required\n")
		flag.Usage()
		return fmt.Errorf("action is required")
	}

	return runMigration(*dir, *dbURL, *action)
}

func runMigration(dir, dbURL, action string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	echan := make(chan struct{}, 1)
	cerr := make(chan error, 1)

	go func(ctx context.Context) {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		signal.Notify(sigint, syscall.SIGTERM)
		<-sigint
		fmt.Fprintln(os.Stderr, "exiting")
		echan <- struct{}{}
	}(ctx)

	go func(ctx context.Context) {
		n, executed, err := Run(ctx, dir, dbURL, action)
		switch strings.Fields(action)[0] {
		case "status":
			fmt.Printf("check migrations located in %v\n", dir)
			fmt.Printf("%v needs to be executed\n", n)
			for _, e := range executed {
				fmt.Printf("%v\n", e)
			}
		case "up", "down":
			fmt.Printf("exec migrations located in %v\n", dir)
			fmt.Printf("executed %v migrations\n", n)
			for _, e := range executed {
				fmt.Printf("%v SUCCESS\n", e)
			}
		}
		if err != nil {
			cerr <- err
			return
		}
		echan <- struct{}{}
	}(ctx)

	select {
	case err := <-cerr:
		return err
	case <-echan:
		return nil
	}
}

func main() {
	if err := Execute(); err != nil {
		log.Fatal(err)
	}
}
