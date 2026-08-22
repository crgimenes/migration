package main

// keikiban (the house PostgreSQL dashboard) already holds the user's
// database connections. Reading them - never writing - lets the
// Connection screen offer those databases as starting points, so using
// the two tools together needs no re-typing. keikiban's config file
// stays keikiban's; migration only parses it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/crgimenes/filo"
)

// keikibanConfigPath mirrors keikiban's own resolution exactly: a local
// keikiban_init.filo wins, otherwise XDG_CONFIG_HOME/keikiban/init.filo
// with ~/.config as the default. keikiban deliberately uses XDG on
// every platform (unlike migration's os.UserConfigDir), so this must
// not "fix" that. Swappable for tests.
var keikibanConfigPath = defaultKeikibanConfigPath

func defaultKeikibanConfigPath() (string, error) {
	local := "keikiban_init.filo"
	_, err := os.Stat(local)
	if err == nil {
		return local, nil
	}

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve keikiban config path: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "keikiban", "init.filo"), nil
}

// keikibanDatabases lists the databases keikiban knows. A missing file
// means keikiban is not in use here: no suggestions, no error.
func keikibanDatabases() ([]SavedConnection, error) {
	path, err := keikibanConfigPath()
	if err != nil {
		return nil, err
	}

	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read keikiban config %s: %w", path, err)
	}

	return parseKeikibanConfig(string(b))
}

// parseKeikibanConfig runs the Filo source collecting keikiban's
// (database url [title]) forms. The dir stays empty: migrations live
// per project, not per database, so the user completes that half.
func parseKeikibanConfig(src string) ([]SavedConnection, error) {
	if !hasCode(src) {
		return nil, nil
	}

	f := filo.New()
	defer f.Close()

	var conns []SavedConnection
	err := f.RegisterBuiltin("database", func(_ context.Context, args []filo.Value) (filo.Value, error) {
		if len(args) < 1 {
			return filo.VBool(false), errors.New("database: url is required")
		}
		dbURL, err := args[0].AsString()
		if err != nil || dbURL == "" {
			return filo.VBool(false), errors.New("database: url must be a non-empty string")
		}

		title := ""
		if len(args) >= 2 {
			title, err = args[1].AsString()
			if err != nil {
				return filo.VBool(false), errors.New("database: title must be a string")
			}
		}
		if title == "" {
			title = defaultTitle(dbURL)
		}

		conns = append(conns, SavedConnection{
			URL:       dbURL,
			Title:     title,
			MaskedURL: redactURL(dbURL),
		})
		return filo.VBool(true), nil
	})
	if err != nil {
		return nil, err
	}

	err = f.DoString(src)
	if err != nil {
		return nil, err
	}
	return conns, nil
}
