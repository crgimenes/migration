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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/crgimenes/glaze"
	"github.com/crgimenes/glaze/menu"
	"github.com/crgimenes/migration/core"
	"github.com/crgimenes/migration/diff"
	"github.com/crgimenes/migration/gen"
	"github.com/crgimenes/migration/snapshot"
	"github.com/crgimenes/native/filedialog"
)

//go:embed ui
var uiFS embed.FS

// guiTimeout bounds every database call made from the GUI, so a busy or
// unreachable server degrades to a visible error instead of a frozen
// window.
const guiTimeout = 15 * time.Second

// guiService methods are bound as window.migration_* in the UI. Every
// operation is available on screen; mutating actions (RunUp, RunDown,
// Capture) are two-step by contract: the UI first fetches the exact SQL
// via the matching *Preview method and shows it in the confirmation, so
// nothing runs that the user could not read first.
//
// Since v5 the GUI is the no-argument default, so it must open without
// a target: dbURL/dir start from flags/env and can be (re)set from the
// UI via Connect.
type guiService struct {
	mu    sync.Mutex
	dbURL string
	dir   string
	// w re-enters the UI thread for native panels; nil in headless tests.
	w glaze.WebView
}

type guiInfo struct {
	Database   string `json:"database"`
	Dir        string `json:"dir"`
	Version    string `json:"version"`
	Postgres   bool   `json:"postgres"`
	Configured bool   `json:"configured"`
	// Raw form prefills; the redacted Database field is for display.
	FormURL string `json:"form_url"`
	FormDir string `json:"form_dir"`
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

func (s *guiService) target() (dbURL, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dbURL, s.dir
}

// requireTarget gates every data method so an unconfigured GUI degrades
// to a visible "connect first" message instead of a driver error.
func (s *guiService) requireTarget() (dbURL, dir string, err error) {
	dbURL, dir = s.target()
	if dbURL == "" || dir == "" {
		return "", "", errors.New("not connected: fill in the database URL and migrations directory")
	}
	return dbURL, dir, nil
}

func (s *guiService) Info() (guiInfo, error) {
	dbURL, dir := s.target()
	info := guiInfo{
		Database:   redactURL(dbURL),
		Dir:        dir,
		Version:    Version,
		Configured: dbURL != "" && dir != "",
		FormURL:    dbURL,
		FormDir:    dir,
	}
	if dbURL != "" {
		config, err := core.GetDatabaseConfig(dbURL)
		if err == nil {
			info.Postgres = config.Type == core.PostgreSQL
		}
	}
	return info, nil
}

// Connect validates and stores a new target. The database must answer a
// ping and the migrations directory must exist before anything is kept.
func (s *guiService) Connect(dbURL, dir string) (guiInfo, error) {
	if dbURL == "" || dir == "" {
		return guiInfo{}, errors.New("both the database URL and the migrations directory are required")
	}

	config, err := core.GetDatabaseConfig(dbURL)
	if err != nil {
		return guiInfo{}, err
	}
	db, err := core.OpenDatabase(dbURL, config)
	if err != nil {
		return guiInfo{}, err
	}
	closeDB(db)

	fi, err := os.Stat(dir) // #nosec G703 -- the user chooses the directory in their own GUI
	if err != nil {
		return guiInfo{}, fmt.Errorf("migrations directory: %w", err)
	}
	if !fi.IsDir() {
		return guiInfo{}, fmt.Errorf("migrations directory %s is not a directory", dir)
	}

	s.mu.Lock()
	s.dbURL = dbURL
	s.dir = dir
	s.mu.Unlock()

	return s.Info()
}

func (s *guiService) Status() (guiStatus, error) {
	dbURL, dir, err := s.requireTarget()
	if err != nil {
		return guiStatus{}, err
	}

	ctx, cancel := s.guiContext()
	defer cancel()

	config, err := core.GetDatabaseConfig(dbURL)
	if err != nil {
		return guiStatus{}, err
	}
	db, err := core.OpenDatabase(dbURL, config)
	if err != nil {
		return guiStatus{}, err
	}
	defer closeDB(db)

	applied, err := core.GetMigrationMax(ctx, db, config)
	if err != nil {
		return guiStatus{}, err
	}
	pending, files, err := core.RunWithExistingDatabase(ctx, dir, "status", db, config)
	if err != nil {
		return guiStatus{}, err
	}
	if files == nil {
		files = []string{}
	}
	return guiStatus{Applied: applied, Pending: pending, Files: files}, nil
}

func (s *guiService) Drift() (guiChanges, error) {
	dbURL, dir, err := s.requireTarget()
	if err != nil {
		return guiChanges{}, err
	}

	ctx, cancel := s.guiContext()
	defer cancel()

	db, _, err := openPostgres(dbURL)
	if err != nil {
		return guiChanges{}, err
	}
	defer closeDB(db)

	snapVersion, _, _, changes, err := liveDiff(ctx, db, dir)
	if err != nil {
		return guiChanges{}, err
	}

	return guiChanges{
		Snapshot: snapVersion,
		Drift:    len(changes) > 0,
		Changes:  nonNil(changes),
		Summary:  diff.Summarize(changes),
	}, nil
}

func (s *guiService) Snapshots() ([]int, error) {
	_, dir, err := s.requireTarget()
	if err != nil {
		return nil, err
	}

	versions, err := snapshot.Versions(dir)
	if err != nil {
		return nil, err
	}
	if versions == nil {
		versions = []int{}
	}
	return versions, nil
}

func (s *guiService) Report(from, to string) (guiChanges, error) {
	dbURL, dir, err := s.requireTarget()
	if err != nil {
		return guiChanges{}, err
	}

	ctx, cancel := s.guiContext()
	defer cancel()

	a, err := resolvePoint(ctx, dir, dbURL, from)
	if err != nil {
		return guiChanges{}, err
	}
	b, err := resolvePoint(ctx, dir, dbURL, to)
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

type guiSQLPreview struct {
	Files []string `json:"files"`
	SQL   string   `json:"sql"`
}

type guiCapturePreview struct {
	Snapshot int           `json:"snapshot"`
	Drift    bool          `json:"drift"`
	Changes  []diff.Change `json:"changes"`
	Summary  string        `json:"summary"`
	UpSQL    string        `json:"up_sql"`
	DownSQL  string        `json:"down_sql"`
}

func readSQLFiles(files []string) (string, error) {
	var b strings.Builder
	for _, f := range files {
		content, err := os.ReadFile(f) // #nosec G304 -- migration files from the user's own dir
		if err != nil {
			return "", fmt.Errorf("failed to read %s: %w", f, err)
		}
		fmt.Fprintf(&b, "-- %s\n%s\n", filepath.Base(f), strings.TrimRight(string(content), "\n"))
	}
	return b.String(), nil
}

// UpPreview returns the pending migrations and their exact SQL, so the
// confirmation dialog shows precisely what Confirm will run.
func (s *guiService) UpPreview() (guiSQLPreview, error) {
	st, err := s.Status()
	if err != nil {
		return guiSQLPreview{}, err
	}
	sql, err := readSQLFiles(st.Files)
	if err != nil {
		return guiSQLPreview{}, err
	}
	return guiSQLPreview{Files: st.Files, SQL: sql}, nil
}

// RunUp applies every pending migration (the confirmed UpPreview).
func (s *guiService) RunUp() (guiStatus, error) {
	dbURL, dir, err := s.requireTarget()
	if err != nil {
		return guiStatus{}, err
	}

	ctx, cancel := s.guiContext()
	defer cancel()

	_, _, err = core.Run(ctx, dir, dbURL, "up")
	if err != nil {
		return guiStatus{}, err
	}
	maybeAutoSnapshot(ctx, dbURL, dir, "up", true)
	return s.Status()
}

// DownPreview returns the down file of the newest applied migration.
func (s *guiService) DownPreview() (guiSQLPreview, error) {
	dbURL, dir, err := s.requireTarget()
	if err != nil {
		return guiSQLPreview{}, err
	}

	ctx, cancel := s.guiContext()
	defer cancel()

	config, err := core.GetDatabaseConfig(dbURL)
	if err != nil {
		return guiSQLPreview{}, err
	}
	db, err := core.OpenDatabase(dbURL, config)
	if err != nil {
		return guiSQLPreview{}, err
	}
	defer closeDB(db)

	applied, err := core.GetMigrationMax(ctx, db, config)
	if err != nil {
		return guiSQLPreview{}, err
	}
	if applied == 0 {
		return guiSQLPreview{}, errors.New("no applied migration to revert")
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.down.sql"))
	if err != nil {
		return guiSQLPreview{}, err
	}
	for _, f := range files {
		if core.FileVersion(f) == applied {
			sql, readErr := readSQLFiles([]string{f})
			if readErr != nil {
				return guiSQLPreview{}, readErr
			}
			return guiSQLPreview{Files: []string{f}, SQL: sql}, nil
		}
	}
	return guiSQLPreview{}, fmt.Errorf("no down file found for applied version %d", applied)
}

// RunDown reverts the newest applied migration (the confirmed
// DownPreview) - one at a time in the GUI, deliberately.
func (s *guiService) RunDown() (guiStatus, error) {
	dbURL, dir, err := s.requireTarget()
	if err != nil {
		return guiStatus{}, err
	}

	ctx, cancel := s.guiContext()
	defer cancel()

	_, _, err = core.Run(ctx, dir, dbURL, "down 1")
	if err != nil {
		return guiStatus{}, err
	}
	maybeAutoSnapshot(ctx, dbURL, dir, "down", true)
	return s.Status()
}

// CapturePreview generates the migration pair the drift would produce,
// without writing anything.
func (s *guiService) CapturePreview() (guiCapturePreview, error) {
	dbURL, dir, err := s.requireTarget()
	if err != nil {
		return guiCapturePreview{}, err
	}

	ctx, cancel := s.guiContext()
	defer cancel()

	db, _, err := openPostgres(dbURL)
	if err != nil {
		return guiCapturePreview{}, err
	}
	defer closeDB(db)

	snapVersion, snap, live, changes, err := liveDiff(ctx, db, dir)
	if err != nil {
		return guiCapturePreview{}, err
	}

	preview := guiCapturePreview{
		Snapshot: snapVersion,
		Drift:    len(changes) > 0,
		Changes:  nonNil(changes),
		Summary:  diff.Summarize(changes),
	}
	if preview.Drift {
		preview.UpSQL, preview.DownSQL = gen.Generate(changes, snap, live)
	}
	return preview, nil
}

// PickDirectory opens the OS folder panel for the migrations directory.
// filedialog requires the main thread, and glaze runs bound methods on
// a goroutine, so the call hops over via Dispatch. Empty means the user
// cancelled; the UI keeps the field as it was.
func (s *guiService) PickDirectory(current string) (string, error) {
	if s.w == nil {
		return "", errors.New("no window available for a native panel")
	}

	result := make(chan string, 1)
	s.w.Dispatch(func() {
		result <- filedialog.PickDirectory(filedialog.Options{
			Title:     "Choose the migrations directory",
			Directory: current,
		})
	})
	return <-result, nil
}

var captureName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Capture writes the previewed migration pair (the confirmed
// CapturePreview).
func (s *guiService) Capture(name string) (*captureOutcome, error) {
	if name == "" {
		name = "captured_changes"
	}
	if !captureName.MatchString(name) {
		return nil, errors.New("name must be lowercase letters, digits, _ or -")
	}

	dbURL, dir, err := s.requireTarget()
	if err != nil {
		return nil, err
	}

	ctx, cancel := s.guiContext()
	defer cancel()

	db, config, err := openPostgres(dbURL)
	if err != nil {
		return nil, err
	}
	defer closeDB(db)

	return captureDrift(ctx, db, config, dir, name)
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
	evalAction := func(action string) func() {
		return func() {
			w.Eval(fmt.Sprintf("migrationMenu(%q)", action))
		}
	}

	items := []menu.Item{
		{Title: "migration", Submenu: []menu.Item{
			{Title: "Quit migration", Shortcut: "cmd+q", OnClick: w.Terminate},
		}},
	}
	if runtime.GOOS == "darwin" {
		// The Edit menu is not cosmetic on macOS: its native selectors
		// are the only route Cocoa gives Cmd+X/C/V/A into the
		// WKWebView's editing commands (keikiban/inro lesson).
		items = append(items, menu.Item{Title: "File", Submenu: []menu.Item{
			{Title: "Close Window", Shortcut: "cmd+w", Selector: "performClose:"},
		}})
		items = append(items, menu.Item{Title: "Edit", Submenu: []menu.Item{
			{Title: "Undo", Shortcut: "cmd+z", Selector: "undo:"},
			{Title: "Redo", Shortcut: "cmd+shift+z", Selector: "redo:"},
			{Separator: true},
			{Title: "Cut", Shortcut: "cmd+x", Selector: "cut:"},
			{Title: "Copy", Shortcut: "cmd+c", Selector: "copy:"},
			{Title: "Paste", Shortcut: "cmd+v", Selector: "paste:"},
			{Title: "Select All", Shortcut: "cmd+a", Selector: "selectAll:"},
		}})
	}
	items = append(items, menu.Item{Title: "View", Submenu: []menu.Item{
		{Title: "Status", Shortcut: "cmd+1", OnClick: evalAction("screen:status")},
		{Title: "Drift", Shortcut: "cmd+2", OnClick: evalAction("screen:drift")},
		{Title: "Report", Shortcut: "cmd+3", OnClick: evalAction("screen:report")},
		{Title: "Connection", Shortcut: "cmd+4", OnClick: evalAction("screen:connection")},
		{Separator: true},
		{Title: "Refresh", Shortcut: "cmd+r", OnClick: evalAction("refresh")},
	}})

	_, err := menu.Set(items, menu.Options{Window: w.Window()})
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

	_, err = glaze.BindMethods(w, "migration", &guiService{dbURL: dbURL, dir: dir, w: w})
	if err != nil {
		return err
	}

	w.Navigate(baseURL)
	w.Run()
	return nil
}
