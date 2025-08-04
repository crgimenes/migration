package main

import (
	"context"
	"database/sql"
	"errors"
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
)

// Color constants for terminal output
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"
	ColorBold   = "\033[1m"

	// Bright colors
	ColorBrightRed    = "\033[91m"
	ColorBrightGreen  = "\033[92m"
	ColorBrightYellow = "\033[93m"
	ColorBrightBlue   = "\033[94m"
	ColorBrightPurple = "\033[95m"
	ColorBrightCyan   = "\033[96m"
)

// isColorSupported checks if the terminal supports color output
func isColorSupported() bool {
	term := os.Getenv("TERM")
	return term != "dumb" && term != ""
}

// Colored output functions
func colorize(text, color string) string {
	if !isColorSupported() {
		return text
	}
	return color + text + ColorReset
}

func printSuccess(text string) string {
	return colorize(text, ColorBrightGreen)
}

func printError(text string) string {
	return colorize(text, ColorBrightRed)
}

func printWarning(text string) string {
	return colorize(text, ColorBrightYellow)
}

func printInfo(text string) string {
	return colorize(text, ColorBrightBlue)
}

func printHeader(text string) string {
	return colorize(text, ColorBold+ColorBrightCyan)
}

func printHighlight(text string) string {
	return colorize(text, ColorBrightPurple)
}

// printBanner displays a nice banner for the migration tool
func printBanner() {
	banner := `
╔══════════════════════════════════════════════╗
║              ↑ Migration Tool                ║
║         Database Migration Assistant         ║
╚══════════════════════════════════════════════╝
`
	fmt.Print(printHeader(banner))
}

// printSeparator prints a visual separator
func printSeparator() {
	fmt.Printf("%s\n", printInfo("────────────────────────────────────────────"))
}

// formatDuration formats a duration in a human-readable way
func formatFileSize(filename string) string {
	if info, err := os.Stat(filename); err == nil {
		size := info.Size()
		if size < 1024 {
			return fmt.Sprintf("(%d bytes)", size)
		} else if size < 1024*1024 {
			return fmt.Sprintf("(%.1f KB)", float64(size)/1024)
		} else {
			return fmt.Sprintf("(%.1f MB)", float64(size)/(1024*1024))
		}
	}
	return ""
}

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
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
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
		return nil, fmt.Errorf("unsupported database scheme: %s", u.Scheme)
	}
}

// OpenDatabase opens a database connection with the appropriate driver
func OpenDatabase(dbURL string, config *DatabaseConfig) (*sqlx.DB, error) {
	// For SQLite memory database, use the correct driver format
	if dbURL == "sqlite::memory:" {
		dbURL = ":memory:"
	} else if config.Type == SQLite {
		// For SQLite file databases, extract the path from the URL
		u, err := url.Parse(dbURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse SQLite URL: %w", err)
		}
		dbURL = u.Path
	}

	db, err := sqlx.Open(config.DriverName, dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to ping database: %w (also failed to close: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// CheckAndCreateMigrationsTable ensures the migrations table exists
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

// GetMigrationCount returns the number of executed migrations in the database
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

// GetMigrationMaxTx returns the maximum migration version in the database using a transaction
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

// InsertMigration inserts a migration version into the database
func InsertMigration(ctx context.Context, tx *sqlx.Tx, config *DatabaseConfig, version int) error {
	query := "INSERT INTO schema_migrations (version) VALUES (" + config.Placeholder + ")"
	_, err := tx.ExecContext(ctx, query, version)
	if err != nil {
		return fmt.Errorf("failed to insert migration version %d: %w", version, err)
	}
	return nil
}

// DeleteMigration deletes a migration version from the database
func DeleteMigration(ctx context.Context, tx *sqlx.Tx, config *DatabaseConfig, version int) error {
	query := "DELETE FROM schema_migrations WHERE version = " + config.Placeholder
	_, err := tx.ExecContext(ctx, query, version)
	if err != nil {
		return fmt.Errorf("failed to delete migration version %d: %w", version, err)
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

func version(path string) int {
	_, file := filepath.Split(path)
	v := strings.Split(file, "_")[0]
	ver, _ := strconv.Atoi(v)
	return ver
}

func apply(ctx context.Context, path string, tx *sqlx.Tx) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open migration file %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			// Log warning but don't override original error
			fmt.Fprintf(os.Stderr, "%s failed to close file %s: %v\n", printWarning("● Warning:"), path, closeErr)
		}
	}()

	content, err := io.ReadAll(file)
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

// status checks database status
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

// doDown handles down migrations within a transaction
func doDown(ctx context.Context, m []string, source string, tx *sqlx.Tx, config *DatabaseConfig) (number int, executed []string, err error) {
	n, err := parsePar(m)
	if err != nil {
		return
	}
	number, executed, err = down(ctx, source, 0, n, tx, config)
	return
}

// doUp handles up migrations within a transaction
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

// Run executes migrations with the given action using database abstraction
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
		if closeErr := db.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "%s failed to close database: %v\n", printWarning("● Warning:"), closeErr)
		}
	}()

	return RunWithExistingDatabase(ctx, source, action, db, config)
}

// RunWithExistingDatabase executes migrations with the given action using an existing database connection
func RunWithExistingDatabase(ctx context.Context, source, action string, db *sqlx.DB, config *DatabaseConfig) (int, []string, error) {
	// Ensure migrations table exists before any operation
	err := CheckAndCreateMigrationsTable(ctx, db, config)
	if err != nil {
		return 0, nil, err
	}

	m := strings.Fields(action)
	if len(m) == 0 {
		return 0, nil, errors.New("action cannot be empty")
	}

	// For status operations, no transaction is needed as they are read-only
	if m[0] == "status" {
		return status(ctx, source, db, config)
	}

	// For up and down operations, use a single transaction for all changes
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to begin global transaction: %w", err)
	}
	defer func() {
		// Ignore rollback errors after successful commit
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
		// Transaction will be rolled back automatically by defer
		return 0, nil, err
	}

	// Commit the transaction only if everything succeeded
	if err = tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("failed to commit global transaction: %w", err)
	}

	return number, executed, nil
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
		printBanner()
		fmt.Fprintf(os.Stderr, "%s %s %s\n\n", printInfo("Usage:"), printHighlight(os.Args[0]), printInfo("[options]"))
		fmt.Fprintf(os.Stderr, "%s\n", printInfo("Options:"))
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n%s\n", printInfo("Examples:"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight(os.Args[0]+" -action status"), printInfo("# Check migration status"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight(os.Args[0]+" -action up"), printInfo("# Run all pending migrations"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight(os.Args[0]+" -action \"up 1\""), printInfo("# Run only 1 migration"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight(os.Args[0]+" -action \"down 1\""), printInfo("# Rollback 1 migration"))
	}

	flag.Parse()

	if *version {
		printBanner()
		fmt.Printf("%s %s\n", printInfo("Version:"), printHighlight(Version))
		fmt.Printf("%s %s\n", printInfo("Built for:"), printHighlight("Go 1.24+"))
		fmt.Printf("%s %s\n", printInfo("Supports:"), printHighlight("PostgreSQL, SQLite"))
		return nil
	}

	if *help {
		flag.Usage()
		return nil
	}

	if *dbURL == "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", printError("● Error:"), "database URL is required")
		flag.Usage()
		return fmt.Errorf("database URL is required")
	}

	if *dir == "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", printError("● Error:"), "migrations directory is required")
		flag.Usage()
		return fmt.Errorf("migrations directory is required")
	}

	if *action == "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", printError("● Error:"), "action is required")
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
		fmt.Fprintln(os.Stderr, printWarning("● Exiting..."))
		echan <- struct{}{}
	}(ctx)

	go func(ctx context.Context) {
		n, executed, err := Run(ctx, dir, dbURL, action)
		switch strings.Fields(action)[0] {
		case "status":
			fmt.Printf("\n%s\n", printHeader("● Migration Status"))
			printSeparator()
			fmt.Printf("%s %s\n", printInfo("→ Checking migrations in:"), printHighlight(dir))
			if n == 0 {
				fmt.Printf("%s %s\n\n", printSuccess("● All migrations are up to date!"), "No pending migrations.")
			} else {
				fmt.Printf("%s %s %s\n", printWarning("● Pending migrations:"), printHighlight(fmt.Sprintf("%d", n)), "need to be executed")
				printSeparator()
				for i, e := range executed {
					size := formatFileSize(e)
					fmt.Printf("  %s %s %s %s\n",
						printInfo(fmt.Sprintf("%d.", i+1)),
						printHighlight(filepath.Base(e)),
						printInfo(size),
						printInfo(fmt.Sprintf("(%s)", e)))
				}
				fmt.Println()
			}
		case "up", "down":
			action := strings.Fields(action)[0]
			actionIcon := "↑"
			actionName := "UP"
			if action == "down" {
				actionIcon = "↓"
				actionName = "DOWN"
			}

			fmt.Printf("\n%s %s %s\n", printHeader("● Migration Execution"), actionIcon, actionName)
			printSeparator()
			fmt.Printf("%s %s\n", printInfo("→ Location:"), printHighlight(dir))

			if n == 0 {
				fmt.Printf("%s %s\n\n", printInfo("● Result:"), "No migrations to execute")
			} else {
				fmt.Printf("%s %s %s\n", printSuccess("● Executed:"), printHighlight(fmt.Sprintf("%d", n)), "migrations")
				printSeparator()
				for i, e := range executed {
					size := formatFileSize(e)
					fmt.Printf("  %s %s %s %s %s\n",
						printSuccess("●"),
						printInfo(fmt.Sprintf("%d.", i+1)),
						printHighlight(filepath.Base(e)),
						printInfo(size),
						printSuccess("SUCCESS"))
				}
				fmt.Println()
			}
		}
		if err != nil {
			fmt.Printf("\n%s %s\n", printError("● Error:"), err.Error())
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
