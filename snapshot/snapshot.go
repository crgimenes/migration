// Package snapshot stores introspected schema models as versioned JSON
// files next to the migration SQL, so drift in the live database can be
// detected against the last known-good state.
package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/crgimenes/migration/introspect"
)

// Dir is the snapshot directory inside the migrations dir, so snapshots
// travel with the SQL files they describe.
func Dir(migrationsDir string) string {
	return filepath.Join(migrationsDir, "snapshots")
}

func Path(migrationsDir string, version int) string {
	return filepath.Join(Dir(migrationsDir), fmt.Sprintf("%03d.json", version))
}

// Write stores the model as the snapshot for the given migration
// version and returns the file path.
func Write(migrationsDir string, version int, s *introspect.Schema) (string, error) {
	data, err := s.ToJSON()
	if err != nil {
		return "", fmt.Errorf("failed to serialize snapshot: %w", err)
	}

	// 0755/0644: snapshots live next to the migration SQL and are
	// committed like any source file.
	dir := Dir(migrationsDir)
	err = os.MkdirAll(dir, 0o755) // #nosec G301
	if err != nil {
		return "", fmt.Errorf("failed to create snapshot dir: %w", err)
	}

	path := Path(migrationsDir, version)
	err = os.WriteFile(path, data, 0o644) // #nosec G306
	if err != nil {
		return "", fmt.Errorf("failed to write snapshot: %w", err)
	}
	return path, nil
}

// Load reads the snapshot for a specific version.
func Load(migrationsDir string, version int) (*introspect.Schema, error) {
	path := Path(migrationsDir, version)
	data, err := os.ReadFile(path) // #nosec G304 -- path derives from the user-provided migrations dir
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot %s: %w", path, err)
	}
	return introspect.Load(data)
}

// Versions lists the snapshot versions present, ascending.
func Versions(migrationsDir string) ([]int, error) {
	files, err := filepath.Glob(filepath.Join(Dir(migrationsDir), "*.json"))
	if err != nil {
		return nil, err
	}

	var versions []int
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".json")
		v, convErr := strconv.Atoi(base)
		if convErr != nil {
			continue
		}
		versions = append(versions, v)
	}
	sort.Ints(versions)
	return versions, nil
}

// Latest returns the highest snapshot version and its model.
// The bool is false when no snapshot exists yet.
func Latest(migrationsDir string) (int, *introspect.Schema, bool, error) {
	versions, err := Versions(migrationsDir)
	if err != nil {
		return 0, nil, false, err
	}
	if len(versions) == 0 {
		return 0, nil, false, nil
	}

	last := versions[len(versions)-1]
	s, err := Load(migrationsDir, last)
	if err != nil {
		return 0, nil, false, err
	}
	return last, s, true, nil
}
