package main

import (
	"context"
	"net/url"
	"strings"

	"github.com/jmoiron/sqlx"
	"golang.org/x/xerrors"
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
			CheckTableExistsSQL: `SELECT count(*) FROM information_schema.tables WHERE table_name = 'schema_migrations'`,
			CreateTableSQL:      `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint NOT NULL, CONSTRAINT schema_migrations_pkey PRIMARY KEY (version))`,
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
func OpenDatabase(ctx context.Context, dbURL string) (*sqlx.DB, *DatabaseConfig, error) {
	config, err := GetDatabaseConfig(dbURL)
	if err != nil {
		return nil, nil, err
	}

	// Convert custom SQLite URL format to what the driver expects
	connectionString := dbURL
	if dbURL == "sqlite::memory:" {
		connectionString = ":memory:"
	}

	db, err := sqlx.ConnectContext(ctx, config.DriverName, connectionString)
	if err != nil {
		return nil, nil, xerrors.Errorf("unable to open database: %w", err)
	}

	err = db.PingContext(ctx)
	if err != nil {
		db.Close()
		return nil, nil, xerrors.Errorf("error pinging database: %w", err)
	}

	return db, config, nil
}

// InsertMigration inserts a migration record using database-specific SQL
func InsertMigration(ctx context.Context, n int, tx *sqlx.Tx, config *DatabaseConfig) error {
	var sql string
	switch config.Type {
	case PostgreSQL:
		sql = `INSERT INTO schema_migrations ("version") VALUES ($1)`
	case SQLite:
		sql = `INSERT INTO schema_migrations (version) VALUES (?)`
	}
	_, err := tx.ExecContext(ctx, sql, n)
	return err
}

// DeleteMigration deletes a migration record using database-specific SQL
func DeleteMigration(ctx context.Context, n int, tx *sqlx.Tx, config *DatabaseConfig) error {
	var sql string
	switch config.Type {
	case PostgreSQL:
		sql = `DELETE FROM schema_migrations WHERE "version"=$1`
	case SQLite:
		sql = `DELETE FROM schema_migrations WHERE version=?`
	}
	_, err := tx.ExecContext(ctx, sql, n)
	return err
}

// SchemaMigrationsExists checks if the schema_migrations table exists
func SchemaMigrationsExists(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) (bool, error) {
	s := struct {
		Count int `db:"count"`
	}{}
	var sql string
	switch config.Type {
	case PostgreSQL:
		sql = `SELECT count(*) as count FROM information_schema.tables WHERE table_name = 'schema_migrations'`
	case SQLite:
		sql = `SELECT count(*) as count FROM sqlite_master WHERE type='table' AND name='schema_migrations'`
	}
	err := db.GetContext(ctx, &s, sql)
	if err != nil {
		return false, err
	}
	return s.Count > 0, nil
}

// CreateMigrationTable creates the schema_migrations table
func CreateMigrationTable(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) error {
	_, err := db.ExecContext(ctx, config.CreateTableSQL)
	return err
}

// GetMigrationMax returns the highest migration version number
func GetMigrationMax(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) (int, error) {
	s := struct {
		Max int `db:"m"`
	}{}
	var sql string
	switch config.Type {
	case PostgreSQL:
		sql = `SELECT coalesce(max("version"),0) AS m FROM schema_migrations`
	case SQLite:
		sql = `SELECT coalesce(max(version),0) AS m FROM schema_migrations`
	}
	err := db.GetContext(ctx, &s, sql)
	return s.Max, err
}

// InitSchemaMigrations initializes the schema_migrations table if it doesn't exist
func InitSchemaMigrations(ctx context.Context, db *sqlx.DB, config *DatabaseConfig) error {
	exists, err := SchemaMigrationsExists(ctx, db, config)
	if err != nil {
		return err
	}
	if !exists {
		err = CreateMigrationTable(ctx, db, config)
	}
	return err
}
