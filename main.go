package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
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
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"
	ColorBold   = "\033[1m"

	ColorBrightRed    = "\033[91m"
	ColorBrightGreen  = "\033[92m"
	ColorBrightYellow = "\033[93m"
	ColorBrightBlue   = "\033[94m"
	ColorBrightPurple = "\033[95m"
	ColorBrightCyan   = "\033[96m"
)

func isColorSupported() bool {
	term := os.Getenv("TERM")
	return term != "dumb" && term != ""
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

func printBanner() {
	banner := `
╔══════════════════════════════════════════════╗
║              ↑ Migration Tool                ║
║         Database Migration Assistant         ║
╚══════════════════════════════════════════════╝
`
	fmt.Print(printHeader(banner))
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
// executed for up/down and pending for status.
type jsonResult struct {
	Action string   `json:"action"`
	Count  int      `json:"count"`
	Files  []string `json:"files"`
	OK     bool     `json:"ok"`
	Error  string   `json:"error,omitempty"`
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
	)

	flag.Usage = func() {
		printBanner()
		fmt.Fprintf(os.Stderr, "%s %s\n\n", printInfo("Usage:"), printHighlight(os.Args[0]+" [options] <command> [args]"))
		fmt.Fprintf(os.Stderr, "%s\n", printInfo("Commands:"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight("(none)"), printInfo("# Open the GUI (default since v5)"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight("up [n] | down [n] | status"), printInfo("# Run or inspect migrations"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight("snapshot"), printInfo("# Record the live schema (PostgreSQL)"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight("diff"), printInfo("# Show drift vs the last snapshot (exit 2 when found)"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight("capture [name]"), printInfo("# Turn drift into an up/down migration pair"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight("report <from> <to>"), printInfo("# Diff two points: snapshot version, `live`, or URL"))
		fmt.Fprintf(os.Stderr, "\n%s\n", printInfo("Options:"))
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n%s\n", printInfo("Examples:"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight(os.Args[0]+" status"), printInfo("# Check migration status"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight(os.Args[0]+" up"), printInfo("# Run all pending migrations"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight(os.Args[0]+" -json diff"), printInfo("# Drift check for scripts and agents"))
		fmt.Fprintf(os.Stderr, "  %s %s\n", printHighlight(os.Args[0]+" report 3 live"), printInfo("# What changed since snapshot 3"))
	}

	flag.Parse()

	if *version {
		printBanner()
		fmt.Printf("%s %s\n", printInfo("Version:"), printHighlight(Version))
		fmt.Printf("%s %s\n", printInfo("Supports:"), printHighlight("PostgreSQL, SQLite"))
		return nil
	}

	if *help {
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
	fmt.Fprintf(os.Stderr, "%s %s\n", printError("● Error:"), missing)
	flag.Usage()
	return fmt.Errorf("%s", missing)
}

func runMigration(ctx context.Context, dir, dbURL, action string, jsonOut, noSnap bool) error {
	n, executed, err := core.Run(ctx, dir, dbURL, action)

	var reportErr error
	if jsonOut {
		reportErr = reportJSON(action, n, executed, err)
	} else {
		reportErr = reportHuman(dir, action, n, executed, err)
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

func reportJSON(action string, n int, executed []string, err error) error {
	result := jsonResult{
		Action: strings.Fields(action)[0],
		Count:  n,
		Files:  executed,
		OK:     err == nil,
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

func reportHuman(dir, action string, n int, executed []string, err error) error {
	switch strings.Fields(action)[0] {
	case "status":
		fmt.Printf("\n%s\n", printHeader("● Migration Status"))
		printSeparator()
		fmt.Printf("%s %s\n", printInfo("→ Checking migrations in:"), printHighlight(dir))
		if n == 0 && err == nil {
			fmt.Printf("%s %s\n\n", printSuccess("● All migrations are up to date!"), "No pending migrations.")
		} else if err == nil {
			fmt.Printf("%s %s %s\n", printWarning("● Pending migrations:"), printHighlight(fmt.Sprintf("%d", n)), "need to be executed")
			printSeparator()
			printFileList(executed, printInfo("●"))
		}
	case "up", "down":
		verb := strings.Fields(action)[0]
		actionIcon := "↑"
		actionName := "UP"
		if verb == "down" {
			actionIcon = "↓"
			actionName = "DOWN"
		}

		fmt.Printf("\n%s %s %s\n", printHeader("● Migration Execution"), actionIcon, actionName)
		printSeparator()
		fmt.Printf("%s %s\n", printInfo("→ Location:"), printHighlight(dir))

		if n == 0 && err == nil {
			fmt.Printf("%s %s\n\n", printInfo("● Result:"), "No migrations to execute")
		} else if err == nil {
			fmt.Printf("%s %s %s\n", printSuccess("● Executed:"), printHighlight(fmt.Sprintf("%d", n)), "migrations")
			printSeparator()
			printFileList(executed, printSuccess("●"))
		}
	}

	if err != nil {
		fmt.Printf("\n%s %s\n", printError("● Error:"), err.Error())
	}
	return err
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
		log.Fatal(err)
	}
}
