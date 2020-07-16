// Package Sauron provides tools for monitoring changes on existing and new
// files inside a directory.
//
// After installation, the CLI tool should be available as:
//
// 	sauron
//
// Running the command without any parameters will cause sauron to watch the
// current directory for changes. Any new lines appended to any files will be
// printed out.
//
// It is possible to specify which directory to watch:
//
// 	sauron --conf=sauron.conf
//
// Most of the code for this tool is available as a standalone package,
// checkout the https://github.com/kykim79/sauron package.
package main

import (
	"./console"
	"fmt"
	"github.com/rs/zerolog/log"
	"gopkg.in/urfave/cli.v1"
	"os"
)

func main() {
	// Setup the command line application
	app := cli.NewApp()
	app.Name = "sauron"
	app.Usage = "Utility for monitoring files in a directory"

	// Set version and authorship info
	app.Version = "0.2.5"
	app.Author = "Eduardo Trujillo <ed@chromabits.com>, Kwon Young, Kim <kykim79@gmail.com>"

	cli.VersionPrinter = func(c *cli.Context) {
		_, _ = fmt.Fprintf(c.App.Writer, "%v", c.App.Version)
	}

	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "pool",
			Usage: "poll for changes instead of using fsnotify (for tailing)",
		},
		cli.BoolTFlag{
			Name:  "prefix-path",
			Usage: "prefix file path to every output line (default)",
		},
		cli.BoolFlag{
			Name:  "prefix-time",
			Usage: "prefix time to every output line",
		},
		cli.StringFlag{
			Name:  "conf",
			Usage: "config file",
		},
	}

	// Setup the default action. This action will be triggered when no
	// sub-command is provided as an argument
	app.Action = console.MainAction

	// Begin
	if err := app.Run(os.Args); err != nil {
		log.Panic()
	}

}
