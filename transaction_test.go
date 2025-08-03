package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Test to verify if transactions rollback correctly on error
func TestTransactionRollbackOnError(t *testing.T) {
	ctx := context.Background()
	dbURL := "sqlite::memory:"

	// Setup database
	config, err := GetDatabaseConfig(dbURL)
	if err != nil {
		t.Fatalf("Failed to get database config: %v", err)
	}

	db, err := OpenDatabase(dbURL, config)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Logf("Warning: failed to close database: %v", closeErr)
		}
	}()

	// Create temporary directory
	tempDir := t.TempDir()

	// Create a valid migration
	validMigration := filepath.Join(tempDir, "001_valid.up.sql")
	err = os.WriteFile(validMigration, []byte("CREATE TABLE test_table (id INTEGER);"), 0644)
	if err != nil {
		t.Fatalf("Failed to create valid migration: %v", err)
	}

	// Create an invalid migration (SQL with error)
	invalidMigration := filepath.Join(tempDir, "002_invalid.up.sql")
	err = os.WriteFile(invalidMigration, []byte("CREATE TABLE invalid_syntax error;"), 0644)
	if err != nil {
		t.Fatalf("Failed to create invalid migration: %v", err)
	}

	t.Log("Testing transaction with failure...")

	// Try to execute up - should fail and rollback
	n, executed, err := RunWithExistingDatabase(ctx, tempDir, "up", db, config)
	if err == nil {
		t.Fatalf("Expected error when executing migrations, but got success")
	}

	t.Logf("Expected error when executing migrations: %v", err)
	t.Logf("Migrations executed before failure: %d", n)
	t.Logf("Files processed: %v", executed)

	// Verify if rollback worked - should have 0 migrations in table
	count, err := GetMigrationCount(ctx, db, config)
	if err != nil {
		t.Fatalf("Failed to verify count: %v", err)
	}

	t.Logf("Number of migrations in database after rollback: %d", count)
	if count != 0 {
		t.Errorf("Expected 0 migrations after rollback, but found %d", count)
	}

	// Verify if table was not created (rollback should have undone everything)
	var tableExists int
	err = db.GetContext(ctx, &tableExists, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='test_table'")
	if err != nil {
		t.Fatalf("Failed to verify table existence: %v", err)
	}

	if tableExists != 0 {
		t.Errorf("Expected table not to exist after rollback, but it exists")
	}

	t.Log("SUCCESS: Transaction was correctly reverted!")
}
