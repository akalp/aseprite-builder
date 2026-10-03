// aseprite-builder builds and installs Aseprite from its official
// source code.
//
// The main function is intentionally trivial: it wires IO and the
// builder version into the CLI and delegates dispatch. All business
// logic lives in the internal packages.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/aseprite-builder/aseprite-builder/internal/cli"
)

// version is overridable at release time with
// -ldflags "-X main.version=1.2.3".
var version = "0.1.0-dev"

func main() {
	c := &cli.CLI{
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		Stdin:          os.Stdin,
		BuilderVersion: version,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(c.Run(ctx, os.Args[1:]))
}
