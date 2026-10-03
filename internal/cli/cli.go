// Package cli implements the non-interactive command line interface.
// It contains no business logic: every command shells out to the app
// service layer, prints human output or JSON, and maps typed errors to
// friendly messages and exit codes.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/aseprite-builder/aseprite-builder/internal/app"
	"github.com/aseprite-builder/aseprite-builder/internal/aseprite"
	"github.com/aseprite-builder/aseprite-builder/internal/paths"
	"github.com/aseprite-builder/aseprite-builder/internal/platformselector"
	"github.com/aseprite-builder/aseprite-builder/internal/runner"
	"github.com/aseprite-builder/aseprite-builder/internal/state"
)

type exitCode int

func (e exitCode) Error() string { return "" }
func (e exitCode) ExitCode() int { return int(e) }

// CLI is the command line front end. A ServiceFactory can be injected
// for tests so commands run against fake infrastructure.
type CLI struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// BuilderVersion is this executable's version.
	BuilderVersion string
	// ServiceFactory constructs the app service; nil uses the real
	// infrastructure wiring.
	ServiceFactory func(ctx context.Context, builderVersion string, verifyPlatform bool) (*app.Service, error)
}

// Run dispatches os.Args. It returns the process exit code.
func (c *CLI) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		c.printUsage(c.Stdout)
		return 0
	}
	switch args[0] {
	case "install":
		return c.cmdInstall(ctx, args[1:])
	case "update":
		return c.cmdUpdate(ctx, args[1:])
	case "launch":
		return c.cmdLaunch(ctx, args[1:])
	case "repair":
		return c.cmdRepair(ctx, args[1:])
	case "rebuild":
		return c.cmdRebuild(ctx, args[1:])
	case "uninstall":
		return c.cmdUninstall(ctx, args[1:])
	case "doctor":
		return c.cmdDoctor(ctx, args[1:])
	case "version", "-v", "--version":
		return c.cmdVersion(args[1:])
	case "help", "-h", "--help":
		// `aseprite-builder help <cmd>` and `aseprite-builder -h` both
		// work; bare help prints the full top-level table.
		if len(args) > 1 && c.helpCommand(ctx, args[1], args[2:]) {
			return 0
		}
		c.printUsage(c.Stdout)
		return 0
	default:
		fmt.Fprintf(c.Stderr, "unknown command %q\n\n", args[0])
		c.printUsage(c.Stderr)
		return int(exitCode(2))
	}
}

// helpCommand handles `help <command> [args]` by delegating to the
// command's own flag help. Returns false when the subcommand is
// unknown (printing a hint is the caller's job).
func (c *CLI) helpCommand(ctx context.Context, name string, args []string) bool {
	fs, known := commandFlagSet(name)
	if !known {
		fmt.Fprintf(c.Stderr, "unknown command %q\n", name)
		return false
	}
	_ = ctx
	// parseArgs short-circuits on the help flag inside args; without a
	// help flag in args, print the same help anyway.
	printCommandHelp(fs, name, c.Stdout)
	return true
}

// commandFlagSet returns a disposable FlagSet for name (used by the
// `help <command>` dispatcher and the top-level usage table). It
// reports unknown=false for names without flag plumbing.
func commandFlagSet(name string) (*flag.FlagSet, bool) {
	switch name {
	case "install":
		fs, _ := installFlags()
		return fs, true
	case "update":
		fs, _ := updateFlags()
		return fs, true
	case "rebuild":
		fs, _ := rebuildFlags()
		return fs, true
	case "uninstall":
		fs, _ := uninstallFlags()
		return fs, true
	case "doctor":
		fs, _ := doctorFlags()
		return fs, true
	case "launch", "repair", "version":
		return plainFlags(name), true
	default:
		return nil, false
	}
}

// printUsage renders the top-level help to w: every command with its
// flags inline, generated from the same FlagSet definitions the
// commands parse — the listing cannot drift from behavior.
func (c *CLI) printUsage(w io.Writer) {
	fmt.Fprint(w, `aseprite-builder — build and install Aseprite from official source

Unofficial third-party tool. Aseprite is a project of Igara Studio S.A.;
this tool does not redistribute Aseprite and compiles it locally.

Usage:
  aseprite-builder <command> [flags]

Commands and flags:
`)
	names := []string{"install", "update", "rebuild", "launch", "repair", "uninstall", "doctor", "version"}
	width := 0
	for _, n := range names {
		if l := utf8.RuneCountInString(n); l > width {
			width = l
		}
	}
	for _, n := range names {
		fmt.Fprintf(w, "  %s  %s\n", padTo(n, width), commandSummaries[n])
		fs, known := commandFlagSet(n)
		if !known {
			continue
		}
		for _, line := range flagLines(fs) {
			fmt.Fprintf(w, "        %s\n", line)
		}
	}
	fmt.Fprint(w, `
Run "aseprite-builder <command> --help" for per-command details.
`)
}

// serviceFor constructs the app service (real or injected factory).
func (c *CLI) serviceFor(ctx context.Context, verifyPlatform bool) (*app.Service, error) {
	if c.ServiceFactory != nil {
		return c.ServiceFactory(ctx, c.BuilderVersion, verifyPlatform)
	}
	return newService(ctx, c.BuilderVersion, verifyPlatform)
}

func (c *CLI) progressPrinter(quiet bool) app.ProgressSink {
	hud := c.newHUD(quiet)
	if hud == nil {
		return nil
	}
	return hud.Sink()
}

// newHUD builds the in-place progress table printer for a pipeline run.
// quiet suppresses progress rendering entirely (used by tests). The
// caller owns the HUD lifetime; Close must be invoked after the run.
func (c *CLI) newHUD(quiet bool) *HUD {
	if quiet {
		return nil
	}
	return NewHUD(c.Stderr)
}

// newService builds the app service from real infrastructure.
func newService(ctx context.Context, builderVersion string, verifyPlatform bool) (*app.Service, error) {
	layout, err := paths.New("")
	if err != nil {
		return nil, err
	}
	if err := layout.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("prepare user directories: %w", err)
	}
	plat, err := platformselector.NewForRunningSystem(builderVersion)
	if verifyPlatform && err != nil {
		if errors.Is(err, platformselector.ErrUnsupportedPlatform) {
			return nil, fmt.Errorf("aseprite-builder does not support %s yet", runtime.GOOS)
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	r := runner.NewConcreteRunner()
	rel := aseprite.NewGithubReleaseProvider()
	store := state.Store{Dir: layout.State}

	return app.New(app.Options{
		BuilderVersion: builderVersion,
		Layout:         layout,
		Platform:       plat,
		Releases:       rel,
		Runner:         r,
		Store:          store,
	})
}

// licenseNotice is printed before the first installation, per section 7
// of the project spec. Concise, no blocking prompt.
const licenseNotice = `
Notice — aseprite-builder
  · This builder is unofficial; it is not affiliated with or endorsed
    by Aseprite or Igara Studio.
  · Aseprite source code will be downloaded and compiled on this
    machine. Nothing is redistributed by aseprite-builder.
  · Your use of Aseprite remains subject to its license. See
    https://www.aseprite.org/faq/#can-i-compile-aseprite-myself
`

// confirm asks the user a yes/no question on stdin. An explicit --yes
// flag skips interactive prompts in automation.
func (c *CLI) confirm(prompt string) bool {
	reader := bufio.NewReader(c.Stdin)
	fmt.Fprintf(c.Stderr, "%s [y/N]: ", prompt)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(c.Stderr, "no input — assuming no")
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// friendlyDownloadError turns network failures into actionable text.
func friendlyDownloadError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "connection refused"):
		return "aseprite-builder could not reach the internet. Check your network connection\nand try again."
	case strings.Contains(msg, "rate limited"):
		return "GitHub's API rate limit was hit. Aseprite metadata could not be resolved\nright now; try again later."
	case strings.Contains(msg, "sha256 mismatch"):
		return "A downloaded archive failed verification and was discarded.\nNothing was installed. Please try again."
	case strings.Contains(msg, "unexpected HTTP status 404"):
		return "The requested upstream artifact was not found; the upstream release page\nmay have changed."
	default:
		return ""
	}
}

// brief extracts n lines.
func brief(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, "\n")
	if out == "" {
		return ""
	}
	return "\n\n   " + strings.Join(strings.Split(out, "\n"), "\n   ")
}

func firstLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	out := lines[0]
	if len(lines) > 1 {
		out += " (more details in the operation log)"
	}
	return out
}

// cmdInstall implements `aseprite-builder install`.
func (c *CLI) cmdInstall(ctx context.Context, args []string) int {
	fs, v := installFlags()
	if code, ok := parseArgs("install", fs, args, c.Stdout, c.Stderr); !ok {
		return code
	}

	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}

	fmt.Fprintln(c.Stderr, strings.TrimSpace(licenseNotice))

	opts := app.InstallOptions{
		Version:               strings.TrimSpace(*v.Version),
		Channel:               aseprite.ChannelStable,
		ForceOverwriteUnowned: *v.Force,
		LaunchAfterInstall:    *v.Launch,
		KeepWorkspace:         *v.KeepWorkspace,
	}
	if !*v.Yes && !c.confirm("Continue and download the official Aseprite source?") {
		fmt.Fprintln(c.Stderr, "install cancelled; nothing was changed")
		return int(exitCode(1))
	}

	hud := c.newHUD(false)
	result, err := svc.Install(ctx, opts, hud.Sink())
	if hud != nil {
		hud.Close(err == nil)
	}
	if err != nil {
		c.printServiceError(err)
		if result.LogPath != "" {
			fmt.Fprintf(c.Stderr, "Full details: %s\n", result.LogPath)
			fmt.Fprintln(c.Stderr, "Your existing installation was not modified.")
		}
		return int(exitCode(1))
	}

	fmt.Fprintf(c.Stderr, "\n✅ Aseprite %s installed: %s\n", result.Version, result.AppPath)
	fmt.Fprintf(c.Stderr, "   Launch it from Spotlight, or run: aseprite-builder launch\n")
	return 0
}

// printServiceError maps typed errors to the plain-language failure UX
// required by the spec (never raw compiler output as the main message).
func (c *CLI) printServiceError(err error) {
	fmt.Fprintf(c.Stderr, "\n✗ %s\n", friendlyMessage(err))
}

func friendlyMessage(err error) string {
	switch {
	case errors.Is(err, app.ErrSystemRequirements):
		var sre app.SystemRequirementsError
		ok := errors.As(err, &sre)
		var b strings.Builder
		b.WriteString("This machine is missing some requirements before Aseprite can be built.\n")
		if ok {
			for _, ck := range sre.Checks {
				if ck.OK {
					continue
				}
				fmt.Fprintf(&b, "  ✗ %s\n", ck.Name)
				if ck.Fix != "" {
					fmt.Fprintf(&b, "    → %s\n", ck.Fix)
				}
			}
		}
		b.WriteString("Nothing was installed or modified.")
		return b.String()
	case errors.Is(err, app.ErrUnknownContract):
		var uce app.UnknownContractError
		detail := ""
		if errors.As(err, &uce) && uce.Reason != nil {
			detail = "\n\nDetails: " + uce.Reason.Error()
		}
		return "This Aseprite release uses a build layout that this version of\n" +
			"aseprite-builder does not recognize. To stay safe, nothing was built or\n" +
			"installed. Updating aseprite-builder may add support." + detail
	case errors.Is(err, app.ErrUpToDate):
		return "Aseprite is already up to date."
	case errors.Is(err, app.ErrAlreadyInstalled):
		return "This Aseprite version is already installed."
	case errors.Is(err, app.ErrNotInstalled):
		return "This command needs an aseprite-builder installation (run install first)."
	default:
		var buildErr *aseprite.UpstreamBuildError
		if errors.As(err, &buildErr) {
			return fmt.Sprintf("Aseprite could not be compiled: the upstream build process failed.\n"+
				"Check the operation log for the exact compiler output.%s", brief(buildErr.Tail, 1))
		}
		var contract *aseprite.ContractViolationError
		if errors.As(err, &contract) {
			return "The upstream build did not produce the expected output. This usually means\n" +
				"the Aseprite build contract changed; nothing was installed.\n\n" +
				fmt.Sprintf("Details: %v", contract)
		}
		msg := err.Error()
		if unpack := friendlyDownloadError(err); unpack != "" {
			return unpack
		}
		if strings.Contains(msg, "context canceled") || strings.Contains(msg, "context deadline exceeded") {
			return "Operation cancelled."
		}
		return firstLine(msg)
	}
}
