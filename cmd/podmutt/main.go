package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nomedialoaded/nomediaplayer"
	"github.com/nomedialoaded/nomediaplayer/internal/app"
	"github.com/nomedialoaded/nomediaplayer/internal/control"
	"github.com/nomedialoaded/nomediaplayer/internal/feedback"
	"github.com/nomedialoaded/nomediaplayer/internal/macmedia"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

var version = "dev"

// importOPML is replaceable so tests need not touch the real profile.
var importOPML = app.ImportOPML

func main() {
	app.SetVersion(version)
	ui.Changelog = podmutt.Changelog
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, func() error {
		return macmedia.Run(func() error { return app.Run(os.Stdin, os.Stdout) })
	}, app.Command))
}

func run(args []string, stdout, stderr io.Writer, startUI func() error, command func(string, io.Writer) error) int {
	flags := flag.NewFlagSet("nomediaplayer", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // flag.Parse may otherwise echo sensitive argument values
	showVersion := flags.Bool("version", false, "print version and exit")
	showChangelog := flags.Bool("changelog", false, "print the changelog and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: nomediaplayer [--version | --changelog | --help | play | pause | stop | next | status | import-opml FILE]")
		fmt.Fprintln(stderr, "  --version  Print version and exit\n  --changelog  Print the changelog and exit\n  --help     Show this help")
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintln(stderr, "nomediaplayer: Invalid command-line options. Use nomediaplayer --help for supported commands.")
		return 2
	}
	if flags.NArg() >= 1 && flags.Arg(0) == "import-opml" {
		if *showVersion || flags.NArg() != 2 {
			fmt.Fprintln(stderr, "nomediaplayer: import-opml requires exactly one file path")
			return 2
		}
		if err := importOPML(flags.Arg(1), stdout); err != nil {
			fmt.Fprintln(stderr, "nomediaplayer:", feedback.For("OPML import", err).Text())
			return 1
		}
		return 0
	}
	if flags.NArg() != 0 {
		if flags.NArg() != 1 || *showVersion || !control.Valid(flags.Arg(0)) {
			fmt.Fprintln(stderr, "nomediaplayer: unexpected arguments. Use nomediaplayer --help for supported commands.")
			return 2
		}
		if err := command(flags.Arg(0), stdout); err != nil {
			fmt.Fprintln(stderr, "nomediaplayer:", feedback.For("Playback command", err).Text())
			if errors.Is(err, control.ErrNotRunning) {
				return 3
			}
			return 1
		}
		return 0
	}
	if *showVersion {
		fmt.Fprintln(stdout, "nomediaplayer", version)
		return 0
	}
	if *showChangelog {
		fmt.Fprint(stdout, podmutt.Changelog)
		return 0
	}
	if err := startUI(); err != nil {
		fmt.Fprintln(stderr, "nomediaplayer:", feedback.For("Start or close nomediaplayer", err).Text())
		return 1
	}
	return 0
}
