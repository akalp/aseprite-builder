// aseprite-builder builds and installs Aseprite from its official
// source code.
//
// The main function is intentionally trivial: it wires IO and the
// builder version into the frontends and delegates dispatch. All
// business logic lives in the internal packages. A bare invocation in
// a terminal opens the interactive TUI (spec §30); anything else goes
// to the CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/akalp/aseprite-builder/internal/app"
	"github.com/akalp/aseprite-builder/internal/cli"
	"github.com/akalp/aseprite-builder/internal/tui"
)

// version is overridable at release time with
// -ldflags "-X main.version=1.2.3".
var version = "0.1.0-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	if tui.ShouldOpen(args, os.Stdin, os.Stdout) {
		svc, err := app.NewDefault(version, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			os.Exit(1)
		}
		os.Exit(tui.Run(ctx, svc, version))
	}

	c := &cli.CLI{
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		Stdin:          os.Stdin,
		BuilderVersion: version,
	}
	os.Exit(c.Run(ctx, args))
}
