package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/akalp/aseprite-builder/internal/app"
	"github.com/akalp/aseprite-builder/internal/packages"
)

// stdinIsTTY reports whether the CLI's input is an interactive
// terminal. The stdlib character-device check mirrors the TUI's TTY
// gate; go-isatty is reserved for internal/tui by the architecture
// guard.
func (c *CLI) stdinIsTTY() bool {
	if c.ForceTTY {
		return true
	}
	f, ok := c.Stdin.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ensureSystemPackages runs the explicit-consent package flow required
// before a build on systems with distribution-managed dependencies
// (Linux; spec §16):
//
//   - missing packages are always shown together with the exact
//     copy-paste command;
//   - the command runs visibly, with the terminal attached, only when
//     the user explicitly confirms on a live TTY — never silently and
//     never from --yes (which only skips builder prompts);
//   - afterwards the check is repeated and the flow proceeds only when
//     nothing is missing.
//
// Returns true when the install may proceed. On false it has already
// explained the situation (the terminal is not interactive, or the
// user declined); err is set only for real failures.
func (c *CLI) ensureSystemPackages(ctx context.Context, svc *app.Service) (bool, error) {
	report, err := svc.SystemPackages(ctx)
	if err != nil {
		return false, err
	}
	if len(report.Missing) == 0 {
		return true, nil
	}

	fmt.Fprintln(c.Stderr, "Some system packages required to build Aseprite are missing:")
	for _, pkg := range report.Missing {
		if pkg.What != "" {
			fmt.Fprintf(c.Stderr, "  ✗ %s (%s)\n", pkg.Name, pkg.What)
		} else {
			fmt.Fprintf(c.Stderr, "  ✗ %s\n", pkg.Name)
		}
	}
	fmt.Fprintf(c.Stderr, "\nInstall them with:\n\n  %s\n\n", report.Command)

	if !c.stdinIsTTY() {
		fmt.Fprintln(c.Stderr, "Run that command, then re-run this command. Nothing was changed.")
		return false, nil
	}
	if !c.confirm("Install these system packages now with sudo?") {
		fmt.Fprintln(c.Stderr, "System packages were not installed; nothing was changed.")
		return false, nil
	}
	if err := svc.InstallSystemPackages(ctx, report.Missing); err != nil {
		return false, err
	}

	// Re-check once: only proceed when the packages actually landed.
	report, err = svc.SystemPackages(ctx)
	if err != nil {
		return false, err
	}
	if len(report.Missing) > 0 {
		return false, fmt.Errorf("system packages are still missing after installation: %s",
			strings.Join(packages.Names(report.Missing), ", "))
	}
	fmt.Fprintln(c.Stderr, "✓ System packages installed.")
	return true, nil
}
