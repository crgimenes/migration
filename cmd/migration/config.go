package main

// Saved connections live in a Filo config file, keikiban pattern: the
// file belongs to the user - the app only appends new connections and
// surgically removes a single line on request, never rewriting or
// reformatting anything else.
//
// The path comes from os.UserConfigDir(), the platform's own location
// (Application Support on macOS - which the App Store sandbox remaps
// into the container, so store builds work unchanged - XDG on Linux,
// AppData on Windows). Never ~/.config on Apple.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/crgimenes/filo"
	"github.com/crgimenes/migration/core"
)

type SavedConnection struct {
	URL   string `json:"-"`
	Dir   string `json:"dir"`
	Title string `json:"title"`
	// MaskedURL is URL with the password hidden; the only form that
	// ever reaches the UI or logs.
	MaskedURL string `json:"url"`
}

// configPath is swappable so tests point it at a temp file.
var configPath = defaultConfigPath

func defaultConfigPath() (string, error) {
	local := "migration_init.filo"
	_, err := os.Stat(local)
	if err == nil {
		return local, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	return filepath.Join(base, "migration", "init.filo"), nil
}

// loadConnections reads the saved connections. A missing file is not an
// error: there is simply nothing saved yet.
func loadConnections() ([]SavedConnection, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	conns, err := parseConnections(string(b))
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return conns, nil
}

// parseConnections runs the Filo source and collects the connections it
// declares through the (connection url dir [title]) builtin.
func parseConnections(src string) ([]SavedConnection, error) {
	// A config holding only comments is legitimate: it is what removing
	// the last connection leaves behind, and Filo rejects empty scripts.
	if !hasCode(src) {
		return nil, nil
	}

	f := filo.New()
	defer f.Close()

	var conns []SavedConnection
	err := f.RegisterBuiltin("connection", func(_ context.Context, args []filo.Value) (filo.Value, error) {
		if len(args) < 2 {
			return filo.VBool(false), errors.New("connection: url and dir are required")
		}

		dbURL, err := args[0].AsString()
		if err != nil || dbURL == "" {
			return filo.VBool(false), errors.New("connection: url must be a non-empty string")
		}
		dir, err := args[1].AsString()
		if err != nil || dir == "" {
			return filo.VBool(false), errors.New("connection: dir must be a non-empty string")
		}

		title := ""
		if len(args) >= 3 {
			title, err = args[2].AsString()
			if err != nil {
				return filo.VBool(false), errors.New("connection: title must be a string")
			}
		}
		if title == "" {
			title = defaultTitle(dbURL)
		}

		conns = append(conns, SavedConnection{
			URL:       dbURL,
			Dir:       dir,
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

// appendConnection saves one connection, creating the file on first
// use. Append-only; an already saved url+dir pair is left alone.
func appendConnection(dbURL, dir, title string) error {
	existing, err := loadConnections()
	if err != nil {
		return err
	}
	for _, c := range existing {
		if c.URL == dbURL && c.Dir == dir {
			return nil
		}
	}

	path, err := configPath()
	if err != nil {
		return err
	}
	err = os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	_, statErr := os.Stat(path)
	isNew := errors.Is(statErr, os.ErrNotExist)

	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open config %s: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	var b strings.Builder
	if isNew {
		b.WriteString("; migration configuration\n")
	}
	b.WriteString("(connection ")
	b.WriteString(filoQuote(dbURL))
	b.WriteString(" ")
	b.WriteString(filoQuote(dir))
	if title != "" {
		b.WriteString(" ")
		b.WriteString(filoQuote(title))
	}
	b.WriteString(")\n")

	_, err = f.WriteString(b.String())
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return f.Close()
}

// removeConnection deletes the index-th (connection ...) line, leaving
// every other line of the file untouched. When the file declares
// connections in a shape line surgery cannot edit safely (multi-line or
// computed forms), it refuses instead of guessing.
func removeConnection(index int) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	conns, err := parseConnections(string(b))
	if err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}

	lines := strings.Split(string(b), "\n")
	var connLines []int
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "(connection ") {
			connLines = append(connLines, i)
		}
	}
	if len(connLines) != len(conns) {
		return fmt.Errorf("config %s declares connections in a shape migration cannot edit safely; edit the file directly", path)
	}
	if index < 0 || index >= len(connLines) {
		return fmt.Errorf("connection %d does not exist", index)
	}

	n := connLines[index]
	lines = slices.Delete(lines, n, n+1)
	err = os.WriteFile(filepath.Clean(path), []byte(strings.Join(lines, "\n")), 0o600) // #nosec G703 -- path comes from configPath (platform config dir), not untrusted input
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// defaultTitle names an untitled connection without exposing the URL (a
// URL can carry a password): the database name when the URL states one,
// otherwise the host.
func defaultTitle(dbURL string) string {
	// A SQLite URL is a filesystem path, which url.Parse cannot handle
	// on Windows; name it after the file.
	if strings.HasPrefix(strings.ToLower(dbURL), "sqlite:") {
		source := core.SQLiteDataSource(dbURL)
		if source == ":memory:" {
			return "connection"
		}
		// Both separators, so a Windows path read from a synced config
		// still names the file when this runs on another platform.
		return source[strings.LastIndexAny(source, `/\`)+1:]
	}

	u, err := url.Parse(dbURL)
	if err != nil {
		return "connection"
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name != "" {
		return filepath.Base(name)
	}
	if u.Host != "" {
		return u.Host
	}
	return "connection"
}

// hasCode reports whether the Filo source contains anything beyond
// whitespace and ; comments.
func hasCode(src string) bool {
	src = strings.TrimPrefix(src, "\uFEFF")
	for line := range strings.SplitSeq(src, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, ";") {
			continue
		}
		return true
	}
	return false
}

func filoQuote(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\t", `\t`,
		"\r", `\r`,
	)
	return `"` + r.Replace(s) + `"`
}
