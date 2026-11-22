// Command sp is the snap command-line client.
//
// Each subcommand lives in its own file and registers itself in init with
// register(...), so parallel work on different subcommands never touches the
// same file.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
)

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string) error
}

var commands = map[string]command{}

func register(c command) {
	if _, dup := commands[c.name]; dup {
		panic("sp: duplicate command " + c.name)
	}
	commands[c.name] = c
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sp <command> [flags] [args]")
	fmt.Fprintln(os.Stderr)
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %-8s %s\n", n, commands[n].summary)
	}
	fmt.Fprintln(os.Stderr, "\nRun sp <command> -h for flags.")
}

func main() {
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
		usage()
		os.Exit(2)
	}
	c, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "sp: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := c.run(ctx, os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "sp:", termString(err.Error()))
		os.Exit(1)
	}
}
