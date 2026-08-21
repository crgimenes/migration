package core

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func Test_upFiles(t *testing.T) {
	tests := []struct {
		name      string
		wantFiles []string
		wantErr   bool
		path      string
	}{
		{
			name: "list files",
			path: "testdata",
			wantFiles: []string{
				filepath.Join("testdata", "001_name.up.sql"),
				filepath.Join("testdata", "002_b_name.up.sql"),
				filepath.Join("testdata", "003_a_name.up.sql"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFiles, err := upFiles(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("upFiles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotFiles, tt.wantFiles) {
				t.Errorf("upFiles() = %v, want %v", gotFiles, tt.wantFiles)
			}
		})
	}
}

func Test_downFiles(t *testing.T) {
	tests := []struct {
		name      string
		wantFiles []string
		wantErr   bool
		path      string
	}{
		{
			name: "list files",
			path: "testdata",
			wantFiles: []string{
				filepath.Join("testdata", "003_a_name.down.sql"),
				filepath.Join("testdata", "002_b_name.down.sql"),
				filepath.Join("testdata", "001_name.down.sql"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFiles, err := downFiles(tt.path, 3)
			if (err != nil) != tt.wantErr {
				t.Errorf("downFiles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotFiles, tt.wantFiles) {
				t.Errorf("downFiles() = %v, want %v", gotFiles, tt.wantFiles)
			}
		})
	}
}

func TestRun(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL environment variable not set, skipping PostgreSQL integration test")
	}

	ctx := context.Background()
	source := "./testdata"

	n, exec, err := Run(ctx, "./test", dbURL, "up")
	t.Logf("Result from empty directory: n=%d, exec=%v, err=%v", n, exec, err)
	if err != nil {
		t.Logf("Got error as expected: %v", err)
	}
	if n != 0 {
		t.Errorf("Expected 0 migrations executed, got %d", n)
	}

	n2, exec2, err := Run(ctx, "./core.go", dbURL, "up")
	t.Logf("Result from file path: n=%d, exec=%v, err=%v", n2, exec2, err)
	if n2 != 0 {
		t.Errorf("Expected 0 migrations executed, got %d", n2)
	}

	n, exec, err = Run(ctx, source, dbURL, "up")
	if err != nil {
		t.Fatalf("up migrations failed: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 migrations executed, got %v", n)
	}
	if len(exec) != 3 {
		t.Errorf("expected 3 executed files, got %v", len(exec))
	}

	n, exec, err = Run(ctx, source, dbURL, "status")
	if err != nil {
		t.Fatalf("status check failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 pending migrations, got %v", n)
	}
	if len(exec) != 0 {
		t.Errorf("expected 0 pending files, got %v", len(exec))
	}

	n, exec, err = Run(ctx, source, dbURL, "down")
	if err != nil {
		t.Fatalf("down migrations failed: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 migrations reverted, got %v", n)
	}
	if len(exec) != 3 {
		t.Errorf("expected 3 reverted files, got %v", len(exec))
	}

	n, exec, err = Run(ctx, source, dbURL, "status")
	if err != nil {
		t.Fatalf("status check after down failed: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 pending migrations after down, got %v", n)
	}
	if len(exec) != 3 {
		t.Errorf("expected 3 pending files after down, got %v", len(exec))
	}

	_, _, err = Run(ctx, source, dbURL, "invalid")
	if err == nil {
		t.Error("expected error for invalid command")
	}
}

func TestDatabaseIntegration(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name        string
		dbURL       string
		skipTest    bool
		skipMsg     string
		useTestData bool // Use testdata directory or create temp SQLite-compatible files
	}{
		{
			name:        "PostgreSQL",
			dbURL:       os.Getenv("DATABASE_URL"),
			skipTest:    os.Getenv("DATABASE_URL") == "",
			skipMsg:     "DATABASE_URL environment variable not set",
			useTestData: true, // PostgreSQL can use existing testdata
		},
		{
			name:        "SQLite_Memory",
			dbURL:       "sqlite::memory:",
			skipTest:    false,
			skipMsg:     "",
			useTestData: false, // SQLite needs compatible SQL
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipTest {
				t.Skip(tc.skipMsg)
			}

			var source string
			if tc.useTestData {
				source = "./testdata"
			} else {
				tempDir := t.TempDir()
				source = tempDir

				createSQLiteTestFiles(t, tempDir)
			}

			// For SQLite memory, we need to use persistent connection
			if tc.name == "SQLite_Memory" {
				config, err := GetDatabaseConfig(tc.dbURL)
				if err != nil {
					t.Fatalf("Failed to get database config: %v", err)
				}

				db, err := OpenDatabase(tc.dbURL, config)
				if err != nil {
					t.Fatalf("Failed to open database: %v", err)
				}
				defer func() {
					closeErr := db.Close()
					if closeErr != nil {
						t.Logf("Warning: failed to close database: %v", closeErr)
					}
				}()

				n, exec, err := RunWithExistingDatabase(ctx, source, "up", db, config)
				if err != nil {
					t.Fatalf("up migrations failed: %v", err)
				}
				if n != 3 {
					t.Errorf("expected 3 migrations executed, got %v", n)
				}
				if len(exec) != 3 {
					t.Errorf("expected 3 executed files, got %v", len(exec))
				}

				n, _, err = RunWithExistingDatabase(ctx, source, "status", db, config)
				if err != nil {
					t.Fatalf("status check failed: %v", err)
				}
				if n != 0 {
					t.Errorf("expected 0 pending migrations, got %v", n)
				}

				n, _, err = RunWithExistingDatabase(ctx, source, "down 1", db, config)
				if err != nil {
					t.Fatalf("partial down failed: %v", err)
				}
				if n != 1 {
					t.Errorf("expected 1 migration reverted, got %v", n)
				}

				n, _, err = RunWithExistingDatabase(ctx, source, "status", db, config)
				if err != nil {
					t.Fatalf("status check after partial down failed: %v", err)
				}
				if n != 1 {
					t.Errorf("expected 1 pending migration, got %v", n)
				}

				_, _, _ = RunWithExistingDatabase(ctx, source, "down", db, config)

			} else {
				n, exec, err := Run(ctx, source, tc.dbURL, "up")
				if err != nil {
					t.Fatalf("up migrations failed: %v", err)
				}
				if n != 3 {
					t.Errorf("expected 3 migrations executed, got %v", n)
				}
				if len(exec) != 3 {
					t.Errorf("expected 3 executed files, got %v", len(exec))
				}

				n, _, err = Run(ctx, source, tc.dbURL, "status")
				if err != nil {
					t.Fatalf("status check failed: %v", err)
				}
				if n != 0 {
					t.Errorf("expected 0 pending migrations, got %v", n)
				}

				n, _, err = Run(ctx, source, tc.dbURL, "down 1")
				if err != nil {
					t.Fatalf("partial down failed: %v", err)
				}
				if n != 1 {
					t.Errorf("expected 1 migration reverted, got %v", n)
				}

				n, _, err = Run(ctx, source, tc.dbURL, "status")
				if err != nil {
					t.Fatalf("status check after partial down failed: %v", err)
				}
				if n != 1 {
					t.Errorf("expected 1 pending migration, got %v", n)
				}

				_, _, _ = Run(ctx, source, tc.dbURL, "down")
			}
		})
	}
}

func createSQLiteTestFiles(t *testing.T, tempDir string) {
	files := []struct {
		name    string
		content string
	}{
		{
			"001_name.up.sql",
			"CREATE TABLE IF NOT EXISTS test (id INTEGER PRIMARY KEY);",
		},
		{
			"001_name.down.sql",
			"DROP TABLE IF EXISTS test;",
		},
		{
			"002_b_name.up.sql",
			"ALTER TABLE test ADD COLUMN name TEXT;",
		},
		{
			"002_b_name.down.sql",
			"ALTER TABLE test DROP COLUMN name;",
		},
		{
			"003_a_name.up.sql",
			"CREATE TABLE IF NOT EXISTS test2 (id INTEGER PRIMARY KEY, data TEXT);",
		},
		{
			"003_a_name.down.sql",
			"DROP TABLE IF EXISTS test2;",
		},
	}

	for _, file := range files {
		err := os.WriteFile(filepath.Join(tempDir, file.name), []byte(file.content), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", file.name, err)
		}
	}
}
