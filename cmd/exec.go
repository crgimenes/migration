package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/felipeweb/migration"
	"github.com/urfave/cli"
)

func init() {
	commands = append(commands, execCmd)
}

var (
	execCmd = cli.Command{
		Name: "exec",
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "url",
				Usage: "DB URL",
			},
			cli.StringFlag{
				Name:  "dir",
				Usage: "Migrations dir",
			},
			cli.StringFlag{
				Name:  "action",
				Usage: "Migrations action",
			},
		},
		Action: migrate,
	}
)

func migrate(c *cli.Context) error {
	var (
		dir    = c.String("dir")
		action = c.String("action")
		dbURL  = c.String("url")
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	echan := make(chan struct{}, 1)
	cerr := make(chan error, 1)
	go func(ctx context.Context) {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		signal.Notify(sigint, syscall.SIGTERM)
		<-sigint
		fmt.Fprintln(c.App.Writer, "exiting")
		echan <- struct{}{}
	}(ctx)
	go func(ctx context.Context) {
		n, executed, err := migration.Run(ctx, dir, dbURL, action)
		switch action {
		case "status":
			fmt.Fprintf(c.App.Writer, "check migrations located in %v\n", dir)
			fmt.Fprintf(c.App.Writer, "%v needs to be executed\n", n)
			for _, e := range executed {
				fmt.Fprintf(c.App.Writer, "%v\n", e)
			}
		case "up", "down":
			fmt.Fprintf(c.App.Writer, "exec migrations located in %v\n", dir)
			fmt.Fprintf(c.App.Writer, "executed %v migrations\n", n)
			for _, e := range executed {
				fmt.Fprintf(c.App.Writer, "%v SUCCESS\n", e)
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
