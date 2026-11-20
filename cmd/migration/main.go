package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/crgimenes/migration/core"
	"github.com/crgimenes/migration/introspect"
	"github.com/crgimenes/migration/snapshot"
)

const (
	ColorReset = "\033[0m"
	ColorBold  = "\033[1m"

	ColorBrightRed    = "\033[91m"
	ColorBrightGreen  = "\033[92m"
	ColorBrightYellow = "\033[93m"
	ColorBrightBlue   = "\033[94m"
	ColorBrightPurple = "\033[95m"
	ColorBrightCyan   = "\033[96m"
)

// stdoutIsTerminal keeps escape codes out of piped or redirected output;
// swappable for tests.
var stdoutIsTerminal = isTerminal(os.Stdout)

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func isColorSupported() bool {
	term := os.Getenv("TERM")
	return stdoutIsTerminal && term != "dumb" && term != "" && os.Getenv("NO_COLOR") == ""
}

func colorize(text, color string) string {
	if !isColorSupported() {
		return text
	}
	return color + text + ColorReset
}

func printSuccess(text string) string {
	return colorize(text, ColorBrightGreen)
}

func printError(text string) string {
	return colorize(text, ColorBrightRed)
}

func printWarning(text string) string {
	return colorize(text, ColorBrightYellow)
}

func printInfo(text string) string {
	return colorize(text, ColorBrightBlue)
}

func printHeader(text string) string {
	return colorize(text, ColorBold+ColorBrightCyan)
}

func printHighlight(text string) string {
	return colorize(text, ColorBrightPurple)
}

func printBanner(w io.Writer) {
	banner := `
╔══════════════════════════════════════════════╗
║              ↑ Migration Tool                ║
║         Database Migration Assistant         ║
╚══════════════════════════════════════════════╝
`
	_, _ = fmt.Fprint(w, printHeader(banner))
}

func printSeparator() {
	fmt.Printf("%s\n", printInfo("────────────────────────────────────────────"))
}

func formatFileSize(filename string) string {
	info, err := os.Stat(filename) // #nosec G703 -- filename comes from the user-provided migrations dir, display only
	if err != nil {
		return ""
	}
	size := info.Size()
	if size < 1024 {
		return fmt.Sprintf("(%d bytes)", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("(%.1f KB)", float64(size)/1024)
	}
	return fmt.Sprintf("(%.1f MB)", float64(size)/(1024*1024))
}

// Version is stamped at build time via -ldflags (see Makefile).
var Version string

// jsonResult is the -json output contract. Count means migrations
// executed for up/down and pending for status; Applied is the highest
// applied version, stated for status (it is what a dashboard like
// keikiban shows next to the pending count).
type jsonResult struct {
	Action  string   `json:"action"`
	Count   int      `json:"count"`
	Applied *int     `json:"applied,omitempty"`
	Files   []string `json:"files"`
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
}

// appliedVersion reads the highest applied migration version; nil when
// the database cannot answer (the status itself already reported the
// real error).
func appliedVersion(ctx context.Context, dbURL string) *int {
	db, config, err := openDB(dbURL)
	if err != nil {
		return nil
	}
	defer closeDB(db)

	v, err := core.GetMigrationMax(ctx, db, config)
	if err != nil {
		return nil
	}
	return &v
}

func Execute() error {
	var (
		dbURL      = flag.String("url", os.Getenv("DATABASE_URL"), "DB URL")
		dir        = flag.String("dir", os.Getenv("MIGRATIONS"), "Migrations dir")
		actionFlag = flag.String("action", os.Getenv("ACTION"), "Migrations action (legacy; prefer the positional form)")
		jsonOut    = flag.Bool("json", false, "Machine-readable JSON output")
		noSnap     = flag.Bool("no-snapshot", false, "Skip the automatic schema snapshot after up/down")
		gui        = flag.Bool("gui", false, "Open the graphical interface")
		debug      = flag.Bool("debug", false, "Enable diagnostics (GUI devtools)")
		version    = flag.Bool("version", false, "Show version")
		help       = flag.Bool("help", false, "Show help")
		helpShort  = flag.Bool("h", false, "Show help")
	)

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		p := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
		printBanner(out)
		p("%s %s\n\n", printInfo("Usage:"), printHighlight(os.Args[0]+" [options] <command> [args]"))
		p("%s\n", printInfo("Commands:"))
		p("  %s %s\n", printHighlight("(none)"), printInfo("# Open the GUI (default since v5)"))
		p("  %s %s\n", printHighlight("up [n] | down [n] | status"), printInfo("# Run or inspect migrations"))
		p("  %s %s\n", printHighlight("snapshot"), printInfo("# Record the live schema (PostgreSQL)"))
		p("  %s %s\n", printHighlight("diff"), printInfo("# Show drift vs the last snapshot (exit 2 when found)"))
		p("  %s %s\n", printHighlight("capture [name]"), printInfo("# Turn drift into an up/down migration pair"))
		p("  %s %s\n", printHighlight("report <from> <to>"), printInfo("# Diff two points: snapshot version, `live`, or URL"))
		p("\n%s\n", printInfo("Options:"))
		flag.PrintDefaults()
		p("\n%s\n", printInfo("Examples:"))
		p("  %s %s\n", printHighlight(os.Args[0]+" status"), printInfo("# Check migration status"))
		p("  %s %s\n", printHighlight(os.Args[0]+" up"), printInfo("# Run all pending migrations"))
		p("  %s %s\n", printHighlight(os.Args[0]+" -json diff"), printInfo("# Drift check for scripts and agents"))
		p("  %s %s\n", printHighlight(os.Args[0]+" report 3 live"), printInfo("# What changed since snapshot 3"))
	}

	flag.Parse()

	if *version {
		printBanner(os.Stdout)
		fmt.Printf("%s %s\n", printInfo("Version:"), printHighlight(Version))
		fmt.Printf("%s %s\n", printInfo("Supports:"), printHighlight("PostgreSQL, SQLite"))
		return nil
	}

	if *help || *helpShort {
		flag.CommandLine.SetOutput(os.Stdout)
		flag.Usage()
		return nil
	}

	action := *actionFlag
	if flag.NArg() > 0 {
		action = strings.Join(flag.Args(), " ")
	}

	// v5 default: no action opens the GUI (missing url/dir become a
	// visible connection form there, not a terminal complaint). The
	// terminal with explicit commands and -json is the automation and
	// AI interface.
	if *gui || action == "" {
		return guiLauncher(*dbURL, *dir, *debug)
	}

	err := checkRequired(*dbURL, *dir, action)
	if err != nil {
		return err
	}

	// Ctrl+C / SIGTERM cancels the context; the in-flight statement is
	// interrupted and the whole batch rolls back.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fields := strings.Fields(action)
	switch fields[0] {
	case "snapshot":
		return cmdSnapshot(ctx, *dbURL, *dir, *jsonOut)
	case "diff":
		return cmdDiff(ctx, *dbURL, *dir, *jsonOut)
	case "capture":
		name := "captured_changes"
		if len(fields) > 1 {
			name = fields[1]
		}
		return cmdCapture(ctx, *dbURL, *dir, name, *jsonOut)
	case "report":
		return cmdReport(ctx, *dbURL, *dir, fields[1:], *jsonOut)
	default:
		return runMigration(ctx, *dir, *dbURL, action, *jsonOut, *noSnap)
	}
}

// guiLauncher is swappable so tests can assert the no-argument default
// without opening a window.
var guiLauncher = runGUI

func checkRequired(dbURL, dir, action string) error {
	verb := strings.Fields(action)[0]

	missing := ""
	switch {
	case dir == "":
		missing = "migrations directory is required"
	case dbURL == "" && verb != "report":
		// report between two snapshot versions works offline.
		missing = "database URL is required"
	}
	if missing == "" {
		return nil
	}
	flag.Usage()
	return errors.New(missing)
}

func runMigration(ctx context.Context, dir, dbURL, action string, jsonOut, noSnap bool) error {
	n, executed, err := core.Run(ctx, dir, dbURL, action)

	var applied *int
	if jsonOut && strings.Fields(action)[0] == "status" && err == nil {
		applied = appliedVersion(ctx, dbURL)
	}

	reportErr := err
	if jsonOut {
		reportErr = reportJSON(action, n, executed, applied, err)
	}
	if !jsonOut && err == nil {
		reportHuman(dir, action, n, executed)
	}

	if reportErr == nil && !noSnap {
		maybeAutoSnapshot(ctx, dbURL, dir, strings.Fields(action)[0], jsonOut)
	}
	return reportErr
}

// maybeAutoSnapshot records the schema after a successful up/down on
// PostgreSQL, so diff/capture always have a fresh baseline. Failure is
// a warning: the migration itself already committed.
func maybeAutoSnapshot(ctx context.Context, dbURL, dir, verb string, jsonOut bool) {
	if verb != "up" && verb != "down" {
		return
	}
	config, err := core.GetDatabaseConfig(dbURL)
	if err != nil || config.Type != core.PostgreSQL {
		return
	}

	db, config, err := openPostgres(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s auto-snapshot failed: %v\n", printWarning("● Warning:"), err)
		return
	}
	defer closeDB(db)

	v, err := core.GetMigrationMax(ctx, db, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s auto-snapshot failed: %v\n", printWarning("● Warning:"), err)
		return
	}
	s, err := introspect.Read(ctx, db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s auto-snapshot failed: %v\n", printWarning("● Warning:"), err)
		return
	}
	path, err := snapshot.Write(dir, v, s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s auto-snapshot failed: %v\n", printWarning("● Warning:"), err)
		return
	}
	if !jsonOut {
		fmt.Printf("%s %s\n", printSuccess("● Snapshot written:"), printHighlight(path))
	}
}

func reportJSON(action string, n int, executed []string, applied *int, err error) error {
	result := jsonResult{
		Action:  strings.Fields(action)[0],
		Count:   n,
		Applied: applied,
		Files:   executed,
		OK:      err == nil,
	}
	if result.Files == nil {
		result.Files = []string{}
	}
	if err != nil {
		result.Error = err.Error()
	}
	encodeErr := json.NewEncoder(os.Stdout).Encode(result)
	if encodeErr != nil {
		return encodeErr
	}
	return err
}

func reportHuman(dir, action string, n int, executed []string) {
	verb := strings.Fields(action)[0]
	switch verb {
	case "status":
		fmt.Printf("\n%s\n", printHeader("● Migration Status"))
		printSeparator()
		fmt.Printf("%s %s\n", printInfo("→ Checking migrations in:"), printHighlight(dir))
		if n == 0 {
			fmt.Printf("%s %s\n\n", printSuccess("● All migrations are up to date!"), "No pending migrations.")
			return
		}
		fmt.Printf("%s %s %s\n", printWarning("● Pending migrations:"), printHighlight(fmt.Sprintf("%d", n)), "need to be executed")
		printSeparator()
		printFileList(executed, printInfo("●"))
	case "up", "down":
		actionIcon := "↑"
		actionName := "UP"
		if verb == "down" {
			actionIcon = "↓"
			actionName = "DOWN"
		}

		fmt.Printf("\n%s %s %s\n", printHeader("● Migration Execution"), actionIcon, actionName)
		printSeparator()
		fmt.Printf("%s %s\n", printInfo("→ Location:"), printHighlight(dir))

		if n == 0 {
			fmt.Printf("%s %s\n\n", printInfo("● Result:"), "No migrations to execute")
			return
		}
		fmt.Printf("%s %s %s\n", printSuccess("● Executed:"), printHighlight(fmt.Sprintf("%d", n)), "migrations")
		printSeparator()
		printFileList(executed, printSuccess("●"))
	}
}

func printFileList(files []string, bullet string) {
	for i, f := range files {
		size := formatFileSize(f)
		fmt.Printf("  %s %s %s %s %s\n",
			bullet,
			printInfo(fmt.Sprintf("%d.", i+1)),
			printHighlight(filepath.Base(f)),
			printInfo(size),
			printInfo(fmt.Sprintf("(%s)", f)))
	}
	fmt.Println()
}

func main() {
	err := Execute()
	if errors.Is(err, errDrift) {
		// The drift details were already printed; exit code 2 lets CI
		// distinguish "drift found" from a hard failure.
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", printError("● Error:"), err)
		os.Exit(1)
	}
}
