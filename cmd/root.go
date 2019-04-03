package cmd

import (
	"fmt"
	"os"

	"github.com/urfave/cli"
)

var (
	app      *cli.App
	commands []cli.Command
	// Version of migration app
	Version string
)

// Execute starts the migration app CLI
func Execute() error {
	app = cli.NewApp()
	app.EnableBashCompletion = true
	app.Name = "Migration Tool"
	app.Author = "Func Cloud"
	app.Copyright = "(c) 2019 Func Cloud"
	app.Commands = commands
	app.Version = Version
	cli.VersionFlag = cli.BoolFlag{
		Name: "version",
	}
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Fprintf(c.App.Writer, "Func Cloud migration service version=%s\n", c.App.Version)
	}
	return app.Run(os.Args)
}
