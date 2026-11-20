package main

// Bookmark tokens persist the migrations-directory access grant across
// launches (a real need only under the macOS App Sandbox; elsewhere the
// tokens are trivially the path). They are app-managed binary state,
// so they live BESIDE the config, never inside it - the Filo file
// belongs to the user and stays human-editable.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/crgimenes/native/bookmark"
)

func bookmarkFile(dir string) (string, error) {
	cfg, err := configPath()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(dir))
	name := hex.EncodeToString(sum[:16]) + ".bookmark"
	return filepath.Join(filepath.Dir(cfg), "bookmarks", name), nil
}

// saveDirBookmark captures the current access grant to dir for the
// next launch. Every successful Connect calls it, which also keeps the
// stored token fresh (bookmarks go stale when the folder moves).
func saveDirBookmark(dir string) error {
	token, err := bookmark.Create(dir)
	if err != nil {
		return err
	}

	path, err := bookmarkFile(dir)
	if err != nil {
		return err
	}
	err = os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		return fmt.Errorf("create bookmarks directory: %w", err)
	}
	err = os.WriteFile(path, token, 0o600)
	if err != nil {
		return fmt.Errorf("write bookmark: %w", err)
	}
	return nil
}

// restoreDirBookmark re-acquires a stored grant before the directory
// is touched. Having no stored token is not an error: dev runs and the
// non-sandbox platforms work without one.
func restoreDirBookmark(dir string) (release func(), err error) {
	path, err := bookmarkFile(dir)
	if err != nil {
		return nil, err
	}

	token, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return func() {}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read bookmark: %w", err)
	}

	_, release, err = bookmark.Resolve(token)
	if err != nil {
		return nil, err
	}
	return release, nil
}
