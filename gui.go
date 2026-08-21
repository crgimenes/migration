package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/crgimenes/glaze"
	"github.com/crgimenes/glaze/menu"
	"github.com/crgimenes/migration/core"
	"github.com/crgimenes/migration/diff"
	"github.com/crgimenes/migration/snapshot"
)

//go:embed ui
var uiFS embed.FS

// guiTimeout bounds every database call made from the GUI, so a busy or
// unreachable server degrades to a visible error instead of a frozen
// window.
const guiTimeout = 15 * time.Second

// guiService methods are bound as window.migration_* in the UI. The GUI
// only performs safe, read-only operations; mutating actions (up, down,
// capture) are shown as CLI commands for the user to run.
type guiService struct {
	dbURL string
	dir   string
}

type guiInfo struct {
	Database string `json:"database"`
	Dir      string `json:"dir"`
	Version  string `json:"version"`
	Postgres bool   `json:"postgres"`
}

type guiStatus struct {
	Applied int      `json:"applied"`
	Pending int      `json:"pending"`
	Files   []string `json:"files"`
}

type guiChanges struct {
	Snapshot int           `json:"snapshot"`
	Drift    bool          `json:"drift"`
	Changes  []diff.Change `json:"changes"`
	Summary  string        `json:"summary"`
	Command  string        `json:"command,omitempty"`
}

// redactURL hides the password so the connection target can be shown in
// the UI without leaking credentials to screenshots.
func redactURL(dbURL string) string {
	u, err := url.Parse(dbURL)
	if err != nil {
		return dbURL
	}
	if u.User == nil {
		return dbURL
	}
	_, hasPassword := u.User.Password()
	if hasPassword {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	s, err := url.PathUnescape(u.String())
	if err != nil {
		return u.String()
	}
	return s
}

func (s *guiService) guiContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), guiTimeout)
}

func (s *guiService) Info() (guiInfo, error) {
	config, err := core.GetDatabaseConfig(s.dbURL)
	if err != nil {
		return guiInfo{}, err
	}
	return guiInfo{
		Database: redactURL(s.dbURL),
		Dir:      s.dir,
		Version:  Version,
		Postgres: config.Type == core.PostgreSQL,
	}, nil
}

func (s *guiService) Status() (guiStatus, error) {
	ctx, cancel := s.guiContext()
	defer cancel()

	config, err := core.GetDatabaseConfig(s.dbURL)
	if err != nil {
		return guiStatus{}, err
	}
	db, err := core.OpenDatabase(s.dbURL, config)
	if err != nil {
		return guiStatus{}, err
	}
	defer closeDB(db)

	applied, err := core.GetMigrationMax(ctx, db, config)
	if err != nil {
		return guiStatus{}, err
	}
	pending, files, err := core.RunWithExistingDatabase(ctx, s.dir, "status", db, config)
	if err != nil {
		return guiStatus{}, err
	}
	if files == nil {
		files = []string{}
	}
	return guiStatus{Applied: applied, Pending: pending, Files: files}, nil
}

func (s *guiService) Drift() (guiChanges, error) {
	ctx, cancel := s.guiContext()
	defer cancel()

	db, _, err := openPostgres(s.dbURL)
	if err != nil {
		return guiChanges{}, err
	}
	defer closeDB(db)

	snapVersion, _, _, changes, err := liveDiff(ctx, db, s.dir)
	if err != nil {
		return guiChanges{}, err
	}

	result := guiChanges{
		Snapshot: snapVersion,
		Drift:    len(changes) > 0,
		Changes:  nonNil(changes),
		Summary:  diff.Summarize(changes),
	}
	if result.Drift {
		// Transparency rule: mutating actions show the exact command
		// instead of a button. The URL comes from DATABASE_URL.
		result.Command = fmt.Sprintf("migration -dir %q capture <name>", s.dir)
	}
	return result, nil
}

func (s *guiService) Snapshots() ([]int, error) {
	versions, err := snapshot.Versions(s.dir)
	if err != nil {
		return nil, err
	}
	if versions == nil {
		versions = []int{}
	}
	return versions, nil
}

func (s *guiService) Report(from, to string) (guiChanges, error) {
	ctx, cancel := s.guiContext()
	defer cancel()

	a, err := resolvePoint(ctx, s.dir, s.dbURL, from)
	if err != nil {
		return guiChanges{}, err
	}
	b, err := resolvePoint(ctx, s.dir, s.dbURL, to)
	if err != nil {
		return guiChanges{}, err
	}

	changes := diff.Compare(a, b)
	return guiChanges{
		Drift:   len(changes) > 0,
		Changes: nonNil(changes),
		Summary: diff.Summarize(changes),
	}, nil
}

func startUIServer() (string, error) {
	ui, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return "", fmt.Errorf("ui files: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}

	srv := &http.Server{Handler: http.FileServerFS(ui), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		_ = srv.Serve(ln)
	}()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("unexpected listener address %v", ln.Addr())
	}
	return fmt.Sprintf("http://127.0.0.1:%d", addr.Port), nil
}

func installGUIMenu(w glaze.WebView) {
	refresh := func() {
		w.Eval("migrationMenu('refresh')")
	}
	_, err := menu.Set([]menu.Item{
		{Title: "migration", Submenu: []menu.Item{
			{Title: "Quit migration", Shortcut: "cmd+q", OnClick: w.Terminate},
		}},
		{Title: "View", Submenu: []menu.Item{
			{Title: "Refresh", Shortcut: "cmd+r", OnClick: refresh},
		}},
	}, menu.Options{Window: w.Window()})
	if err != nil && !errors.Is(err, menu.ErrUnsupported) {
		log.Printf("menu: %v", err)
	}
}

func runGUI(dbURL, dir string, debug bool) error {
	baseURL, err := startUIServer()
	if err != nil {
		return err
	}

	w, err := glaze.New(debug)
	if err != nil {
		return err
	}
	defer w.Destroy()

	w.SetTitle("migration")
	w.SetSize(860, 600, glaze.HintNone)
	w.SetSize(560, 400, glaze.HintMin)

	installGUIMenu(w)

	_, err = glaze.BindMethods(w, "migration", &guiService{dbURL: dbURL, dir: dir})
	if err != nil {
		return err
	}

	w.Navigate(baseURL)
	w.Run()
	return nil
}
