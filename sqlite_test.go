package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteSupport(t *testing.T) {
	ctx := context.Background()

	// Test with SQLite in-memory database
	dbURL := "sqlite::memory:"

	// Get database configuration
	config, err := GetDatabaseConfig(dbURL)
	if err != nil {
		t.Fatalf("Failed to get database config: %v", err)
	}

	// Open database connection that will persist for the whole test
	db, err := OpenDatabase(dbURL, config)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Logf("Warning: failed to close database: %v", closeErr)
		}
	}()

	// Create temporary migration files
	tempDir := t.TempDir()

	// Create test migration files with proper SQL for SQLite
	upFile1 := filepath.Join(tempDir, "001_create_test_table.up.sql")
	downFile1 := filepath.Join(tempDir, "001_create_test_table.down.sql")
	upFile2 := filepath.Join(tempDir, "002_add_index.up.sql")
	downFile2 := filepath.Join(tempDir, "002_add_index.down.sql")

	// Write migration content with SQLite-compatible SQL
	err = os.WriteFile(upFile1, []byte(`
		CREATE TABLE test_table (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`), 0644)
	if err != nil {
		t.Fatalf("Failed to create up migration file: %v", err)
	}

	err = os.WriteFile(downFile1, []byte(`DROP TABLE IF EXISTS test_table;`), 0644)
	if err != nil {
		t.Fatalf("Failed to create down migration file: %v", err)
	}

	err = os.WriteFile(upFile2, []byte(`CREATE INDEX IF NOT EXISTS idx_test_name ON test_table(name);`), 0644)
	if err != nil {
		t.Fatalf("Failed to create second up migration file: %v", err)
	}

	err = os.WriteFile(downFile2, []byte(`DROP INDEX IF EXISTS idx_test_name;`), 0644)
	if err != nil {
		t.Fatalf("Failed to create second down migration file: %v", err)
	}

	// Test status on empty database
	n, executed, err := RunWithExistingDatabase(ctx, tempDir, "status", db, config)
	if err != nil {
		t.Fatalf("Status command failed: %v", err)
	}
	t.Logf("Initial status: %d pending migrations, files: %v", n, executed)
	if n != 2 {
		t.Errorf("Expected 2 pending migrations, got %d", n)
	}
	if len(executed) != 2 {
		t.Errorf("Expected 2 migration files listed, got %d", len(executed))
	}

	// Test running all migrations up
	n, executed, err = RunWithExistingDatabase(ctx, tempDir, "up", db, config)
	if err != nil {
		t.Fatalf("Up command failed: %v", err)
	}
	t.Logf("After up: %d migrations executed, files: %v", n, executed)
	if n != 2 {
		t.Errorf("Expected 2 migrations executed, got %d", n)
	}
	if len(executed) != 2 {
		t.Errorf("Expected 2 migration files executed, got %d", len(executed))
	}

	// Test status after migrations
	n, executed, err = RunWithExistingDatabase(ctx, tempDir, "status", db, config)
	if err != nil {
		t.Fatalf("Status command failed after migration: %v", err)
	}
	t.Logf("Status after up: %d pending migrations, files: %v", n, executed)
	if n != 0 {
		t.Errorf("Expected 0 pending migrations after up, got %d", n)
	}

	// Test running one migration down
	n, executed, err = RunWithExistingDatabase(ctx, tempDir, "down 1", db, config)
	if err != nil {
		t.Fatalf("Down command failed: %v", err)
	}
	t.Logf("After down 1: %d migrations reverted, files: %v", n, executed)
	if n != 1 {
		t.Errorf("Expected 1 migration reverted, got %d", n)
	}

	// Test status after down migration
	n, executed, err = RunWithExistingDatabase(ctx, tempDir, "status", db, config)
	if err != nil {
		t.Fatalf("Status command failed after down migration: %v", err)
	}
	t.Logf("Status after down: %d pending migrations, files: %v", n, executed)
	if n != 1 {
		t.Errorf("Expected 1 pending migration after down, got %d", n)
	}

	// Clean up: run remaining down migration
	_, _, _ = RunWithExistingDatabase(ctx, tempDir, "down", db, config)
}

func TestPostgreSQLURLParsing(t *testing.T) {
	testCases := []struct {
		url            string
		expectedDriver string
		expectedType   DatabaseType
		shouldFail     bool
	}{
		{
			url:            "postgres://user:pass@localhost:5432/dbname",
			expectedDriver: "postgres",
			expectedType:   PostgreSQL,
			shouldFail:     false,
		},
		{
			url:            "postgresql://user:pass@localhost:5432/dbname",
			expectedDriver: "postgres",
			expectedType:   PostgreSQL,
			shouldFail:     false,
		},
		{
			url:            "sqlite::memory:",
			expectedDriver: "sqlite",
			expectedType:   SQLite,
			shouldFail:     false,
		},
		{
			url:            "sqlite:///path/to/db.sqlite",
			expectedDriver: "sqlite",
			expectedType:   SQLite,
			shouldFail:     false,
		},
		{
			url:        "mysql://user:pass@localhost:3306/dbname",
			shouldFail: true,
		},
		{
			url:        "invalid-url",
			shouldFail: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.url, func(t *testing.T) {
			config, err := GetDatabaseConfig(tc.url)

			if tc.shouldFail {
				if err == nil {
					t.Errorf("Expected error for URL %s, but got none", tc.url)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error for URL %s: %v", tc.url, err)
				return
			}

			if config.DriverName != tc.expectedDriver {
				t.Errorf("Expected driver %s, got %s", tc.expectedDriver, config.DriverName)
			}

			if config.Type != tc.expectedType {
				t.Errorf("Expected type %v, got %v", tc.expectedType, config.Type)
			}
		})
	}
}

func TestDatabaseSpecificSQL(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name   string
		dbURL  string
		dbType DatabaseType
	}{
		{
			name:   "SQLite",
			dbURL:  "sqlite::memory:",
			dbType: SQLite,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := GetDatabaseConfig(tc.dbURL)
			if err != nil {
				t.Fatalf("Failed to get database config: %v", err)
			}

			db, err := OpenDatabase(tc.dbURL, config)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer func() {
				if closeErr := db.Close(); closeErr != nil {
					t.Logf("Warning: failed to close database: %v", closeErr)
				}
			}()

			if config.Type != tc.dbType {
				t.Errorf("Expected database type %v, got %v", tc.dbType, config.Type)
			}

			// Test table creation
			err = CheckAndCreateMigrationsTable(ctx, db, config)
			if err != nil {
				t.Errorf("Failed to create migration table: %v", err)
			}

			// Test inserting a migration
			tx, err := db.BeginTxx(ctx, nil)
			if err != nil {
				t.Errorf("Failed to begin transaction: %v", err)
				return
			}

			err = InsertMigration(ctx, tx, config, 1)
			if err != nil {
				if rollbackErr := tx.Rollback(); rollbackErr != nil {
					t.Logf("Warning: failed to rollback transaction: %v", rollbackErr)
				}
				t.Errorf("Failed to insert migration: %v", err)
				return
			}

			err = tx.Commit()
			if err != nil {
				t.Errorf("Failed to commit transaction: %v", err)
				return
			}

			// Test deleting migration
			tx, err = db.BeginTxx(ctx, nil)
			if err != nil {
				t.Errorf("Failed to begin transaction: %v", err)
				return
			}

			err = DeleteMigration(ctx, tx, config, 1)
			if err != nil {
				if rollbackErr := tx.Rollback(); rollbackErr != nil {
					t.Logf("Warning: failed to rollback transaction: %v", rollbackErr)
				}
				t.Errorf("Failed to delete migration: %v", err)
				return
			}

			err = tx.Commit()
			if err != nil {
				t.Errorf("Failed to commit transaction: %v", err)
				return
			}
		})
	}
}
