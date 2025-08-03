package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var (
	// Version of migration app
	Version string
)

// Execute starts the migration app CLI
func Execute() error {
	var (
		dbURL   = flag.String("url", os.Getenv("DATABASE_URL"), "DB URL")
		dir     = flag.String("dir", os.Getenv("MIGRATIONS"), "Migrations dir")
		action  = flag.String("action", os.Getenv("ACTION"), "Migrations action")
		version = flag.Bool("version", false, "Show version")
		help    = flag.Bool("help", false, "Show help")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Migration Tool\n")
		fmt.Fprintf(os.Stderr, "Author: Go Sidekick Team\n")
		fmt.Fprintf(os.Stderr, "Copyright: (c) 2019 Go Sidekick\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *version {
		fmt.Printf("Migration tool version=%s\n", Version)
		return nil
	}

	if *help {
		flag.Usage()
		return nil
	}

	if *dbURL == "" {
		fmt.Fprintf(os.Stderr, "Error: database URL is required\n")
		flag.Usage()
		return fmt.Errorf("database URL is required")
	}

	if *dir == "" {
		fmt.Fprintf(os.Stderr, "Error: migrations directory is required\n")
		flag.Usage()
		return fmt.Errorf("migrations directory is required")
	}

	if *action == "" {
		fmt.Fprintf(os.Stderr, "Error: action is required\n")
		flag.Usage()
		return fmt.Errorf("action is required")
	}

	return runMigration(*dir, *dbURL, *action)
}

func runMigration(dir, dbURL, action string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	echan := make(chan struct{}, 1)
	cerr := make(chan error, 1)

	go func(ctx context.Context) {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		signal.Notify(sigint, syscall.SIGTERM)
		<-sigint
		fmt.Fprintln(os.Stderr, "exiting")
		echan <- struct{}{}
	}(ctx)

	go func(ctx context.Context) {
		n, executed, err := Run(ctx, dir, dbURL, action)
		switch strings.Fields(action)[0] {
		case "status":
			fmt.Printf("check migrations located in %v\n", dir)
			fmt.Printf("%v needs to be executed\n", n)
			for _, e := range executed {
				fmt.Printf("%v\n", e)
			}
		case "up", "down":
			fmt.Printf("exec migrations located in %v\n", dir)
			fmt.Printf("executed %v migrations\n", n)
			for _, e := range executed {
				fmt.Printf("%v SUCCESS\n", e)
			}
		}
		if err != nil {
			cerr <- err
			return
		}
		echan <- struct{}{}
	}(ctx)

	select {
	case err := <-cerr:
		return err
	case <-echan:
		return nil
	}
}
