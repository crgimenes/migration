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

	// Open database connection that will persist for the whole test
	db, config, err := OpenDatabase(ctx, dbURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

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
	n, executed, err := RunWithDatabase(ctx, tempDir, "status", db, config)
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
	n, executed, err = RunWithDatabase(ctx, tempDir, "up", db, config)
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
	n, executed, err = RunWithDatabase(ctx, tempDir, "status", db, config)
	if err != nil {
		t.Fatalf("Status command failed after migration: %v", err)
	}
	t.Logf("Status after up: %d pending migrations, files: %v", n, executed)
	if n != 0 {
		t.Errorf("Expected 0 pending migrations after up, got %d", n)
	}

	// Test running one migration down
	n, executed, err = RunWithDatabase(ctx, tempDir, "down 1", db, config)
	if err != nil {
		t.Fatalf("Down command failed: %v", err)
	}
	t.Logf("After down 1: %d migrations reverted, files: %v", n, executed)
	if n != 1 {
		t.Errorf("Expected 1 migration reverted, got %d", n)
	}

	// Test status after down migration
	n, executed, err = RunWithDatabase(ctx, tempDir, "status", db, config)
	if err != nil {
		t.Fatalf("Status command failed after down migration: %v", err)
	}
	t.Logf("Status after down: %d pending migrations, files: %v", n, executed)
	if n != 1 {
		t.Errorf("Expected 1 pending migration after down, got %d", n)
	}

	// Clean up: run remaining down migration
	RunWithDatabase(ctx, tempDir, "down", db, config)
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
			db, config, err := OpenDatabase(ctx, tc.dbURL)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			if config.Type != tc.dbType {
				t.Errorf("Expected database type %v, got %v", tc.dbType, config.Type)
			}

			// Test table creation
			err = CreateMigrationTable(ctx, db, config)
			if err != nil {
				t.Errorf("Failed to create migration table: %v", err)
			}

			// Test table existence check
			exists, err := SchemaMigrationsExists(ctx, db, config)
			if err != nil {
				t.Errorf("Failed to check table existence: %v", err)
			}
			if !exists {
				t.Errorf("Expected migration table to exist")
			}

			// Test migration max on empty table
			max, err := GetMigrationMax(ctx, db, config)
			if err != nil {
				t.Errorf("Failed to get migration max: %v", err)
			}
			if max != 0 {
				t.Errorf("Expected max migration to be 0, got %d", max)
			}

			// Test inserting a migration
			tx, err := db.Beginx()
			if err != nil {
				t.Fatalf("Failed to begin transaction: %v", err)
			}

			err = InsertMigration(ctx, 1, tx, config)
			if err != nil {
				tx.Rollback()
				t.Errorf("Failed to insert migration: %v", err)
				return
			}

			err = tx.Commit()
			if err != nil {
				t.Errorf("Failed to commit transaction: %v", err)
				return
			}

			// Test getting max after insert
			max, err = GetMigrationMax(ctx, db, config)
			if err != nil {
				t.Errorf("Failed to get migration max after insert: %v", err)
			}
			if max != 1 {
				t.Errorf("Expected max migration to be 1, got %d", max)
			}

			// Test deleting migration
			tx, err = db.Beginx()
			if err != nil {
				t.Fatalf("Failed to begin transaction for delete: %v", err)
			}

			err = DeleteMigration(ctx, 1, tx, config)
			if err != nil {
				tx.Rollback()
				t.Errorf("Failed to delete migration: %v", err)
				return
			}

			err = tx.Commit()
			if err != nil {
				t.Errorf("Failed to commit delete transaction: %v", err)
				return
			}

			// Test getting max after delete
			max, err = GetMigrationMax(ctx, db, config)
			if err != nil {
				t.Errorf("Failed to get migration max after delete: %v", err)
			}
			if max != 0 {
				t.Errorf("Expected max migration to be 0 after delete, got %d", max)
			}
		})
	}
}
