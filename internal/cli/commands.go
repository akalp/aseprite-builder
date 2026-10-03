package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aseprite-builder/aseprite-builder/internal/app"
)

// cmdUpdate implements `aseprite-builder update`.
func (c *CLI) cmdUpdate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	launch := fs.Bool("launch", false, "launch Aseprite after a successful update")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(c.Stderr, "update: %v\n", err)
		return int(exitCode(2))
	}

	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	if !*yes && !c.confirm("Check for updates and install the latest stable Aseprite?") {
		fmt.Fprintln(c.Stderr, "update cancelled; nothing was changed")
		return int(exitCode(1))
	}

	hud := c.newHUD(false)
	result, err := svc.Update(ctx, app.InstallOptions{LaunchAfterInstall: *launch}, hud.Sink())
	hud.Close(err == nil)
	if err != nil {
		if errors.Is(err, app.ErrUpToDate) {
			fmt.Fprintln(c.Stderr, "Aseprite is already up to date.")
			return 0
		}
		c.printServiceError(err)
		if result.LogPath != "" {
			fmt.Fprintf(c.Stderr, "Full details: %s\n", result.LogPath)
			fmt.Fprintln(c.Stderr, "Your existing installation was not modified.")
		}
		return int(exitCode(1))
	}
	fmt.Fprintf(c.Stderr, "\n✅ Updated to Aseprite %s: %s\n", result.Version, result.AppPath)
	return 0
}

// cmdLaunch implements `aseprite-builder launch`.
func (c *CLI) cmdLaunch(ctx context.Context) int {
	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	if err := svc.Launch(ctx); err != nil {
		c.printServiceError(err)
		return int(exitCode(1))
	}
	fmt.Fprintln(c.Stderr, "Aseprite launched.")
	return 0
}

// cmdRepair implements `aseprite-builder repair`.
func (c *CLI) cmdRepair(ctx context.Context) int {
	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	hud := c.newHUD(false)
	result, err := svc.Repair(ctx, hud.Sink())
	hud.Close(err == nil)
	if err != nil {
		c.printServiceError(err)
		if result.LogPath != "" {
			fmt.Fprintf(c.Stderr, "Full details: %s\n", result.LogPath)
		}
		return int(exitCode(1))
	}
	fmt.Fprintf(c.Stderr, "\n✅ %s\n", sdpResult(result))
	return 0
}

func sdpResult(r app.InstallResult) string {
	if r.Version != "" && r.AppPath != "" {
		return fmt.Sprintf("Aseprite %s is installed and healthy at %s", r.Version, r.AppPath)
	}
	return "installation is healthy"
}

// cmdRebuild implements `aseprite-builder rebuild`.
func (c *CLI) cmdRebuild(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("rebuild", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	clean := fs.Bool("clean", false, "start from a fresh source extraction (discards previous build output, keeps your user data)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(c.Stderr, "rebuild: %v\n", err)
		return int(exitCode(2))
	}

	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	hud := c.newHUD(false)
	result, err := svc.Rebuild(ctx, app.RebuildOptions{Clean: *clean}, hud.Sink())
	hud.Close(err == nil)
	if err != nil {
		c.printServiceError(err)
		if result.LogPath != "" {
			fmt.Fprintf(c.Stderr, "Full details: %s\n", result.LogPath)
			fmt.Fprintln(c.Stderr, "Your existing installation was not modified.")
		}
		return int(exitCode(1))
	}
	fmt.Fprintf(c.Stderr, "\n✅ Rebuilt Aseprite %s: %s\n", result.Version, result.AppPath)
	return 0
}

// cmdUninstall implements `aseprite-builder uninstall`.
func (c *CLI) cmdUninstall(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "remove Aseprite.app even if this tool cannot prove it installed it")
	removePrev := fs.Bool("previous", false, "also remove the retained previous version")
	purgeSources := fs.Bool("purge-sources", false, "also remove downloaded Aseprite source trees")
	purgeTools := fs.Bool("purge-tools", false, "also remove downloaded CMake/Ninja caches")
	purgeSkia := fs.Bool("purge-skia", false, "also remove downloaded Skia artifacts")
	purgeLogs := fs.Bool("purge-logs", false, "also remove operation logs")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(c.Stderr, "uninstall: %v\n", err)
		return int(exitCode(2))
	}

	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	prompt := "Remove the aseprite-builder installation of Aseprite?"
	if *purgeSources || *purgeTools || *purgeSkia || *purgeLogs {
		prompt = "Remove Aseprite and the selected aseprite-builder caches?"
	}
	if !c.confirm(prompt) {
		fmt.Fprintln(c.Stderr, "uninstall cancelled; nothing was removed")
		return int(exitCode(1))
	}
	if err := svc.Uninstall(ctx, app.UninstallOptions{
		Force:          *force,
		RemovePrevious: *removePrev,
	}); err != nil {
		c.printServiceError(err)
		return int(exitCode(1))
	}
	removed := []string{"the application"}
	if *removePrev {
		removed = append(removed, "the retained previous version")
	}
	if *purgeSources {
		purgeDir(c, "sources", svc.Layout().Sources)
	}
	if *purgeTools {
		purgeDir(c, "CMake/Ninja tool cache", svc.Layout().Tools)
	}
	if *purgeSkia {
		purgeDir(c, "Skia cache", svc.Layout().Skia)
	}
	if *purgeLogs {
		purgeDir(c, "logs", svc.Layout().Logs)
	}
	fmt.Fprintf(c.Stderr, "\n✅ Removed %s.\n", strings.Join(removed, ", "))
	return 0
}

func purgeDir(c *CLI, what, dir string) {
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(c.Stderr, "note: could not remove %s (%s): %v\n", what, dir, err)
		return
	}
	fmt.Fprintf(c.Stderr, "   removed %s\n", dir)
}

// cmdDoctor implements `aseprite-builder doctor`.
func (c *CLI) cmdDoctor(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(c.Stderr, "doctor: %v\n", err)
		return int(exitCode(2))
	}

	svc, err := c.serviceFor(ctx, false) // doctor works even on unsupported OS
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	report, err := svc.Doctor(ctx)
	if err != nil {
		c.printServiceError(err)
		return int(exitCode(1))
	}
	if *asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(c.Stderr, "doctor: %v\n", err)
			return int(exitCode(1))
		}
		fmt.Fprintln(c.Stdout, string(data))
	} else {
		c.printDoctor(report)
	}
	if report.Status != "healthy" {
		return int(exitCode(1))
	}
	return 0
}

func (c *CLI) printDoctor(r app.DoctorReport) {
	// Build a two-column table sized to the longest label.
	type row struct {
		ok           bool
		name, detail string
	}
	var rows []row
	rowOK := func(name, detail string) { rows = append(rows, row{true, name, detail}) }
	rowFail := func(name, detail string) { rows = append(rows, row{false, name, detail}) }

	rowOK("Operating system", fmt.Sprintf("%s %s", r.System.OS, r.System.OSVersion))
	if r.System.Supported {
		rowOK("Architecture", r.System.Arch)
	} else {
		rowFail("Architecture", r.System.Arch+" (unsupported)")
	}
	for _, c2 := range r.Requirements {
		if c2.OK {
			rowOK(c2.Name, c2.Detail)
		} else {
			rowFail(c2.Name, c2.Fix)
		}
	}
	for _, t := range r.Tools {
		if t.Status == "none" || t.Status == "error" {
			rowFail(t.Name, "unavailable and no downloadable alternative was configured")
		} else {
			rowOK(t.Name, t.Display())
		}
	}
	if r.Skia.Status == "cached" {
		rowOK("Skia", "cached: "+strings.Join(r.Skia.CachedTags, ", "))
	} else {
		rowOK("Skia", "no cached artifact (resolved during install)")
	}
	if r.Installation != nil && r.Installation.HealthyChecked {
		if r.Installation.Healthy {
			rowOK("Installation", fmt.Sprintf("Aseprite %s at %s", r.Installation.InstalledVersion, r.Installation.AppPath))
		} else {
			rowFail("Installation", "broken — run: aseprite-builder repair")
		}
	} else {
		rowOK("Installation", "none (nothing installed yet)")
	}

	nameWidth := len("Installation")
	for _, row := range rows {
		if len(row.name) > nameWidth {
			nameWidth = len(row.name)
		}
	}

	fmt.Fprintln(c.Stdout, "Diagnostics")
	fmt.Fprintln(c.Stdout, "")
	for _, row := range rows {
		mark := "✓"
		if !row.ok {
			mark = "✗"
		}
		fmt.Fprintf(c.Stdout, "%s %-*s  %s\n", mark, nameWidth, row.name, row.detail)
	}
	fmt.Fprintln(c.Stdout, "")
	if r.Status == "healthy" {
		fmt.Fprintln(c.Stdout, "Everything looks good.")
	} else {
		fmt.Fprintln(c.Stdout, "Some checks failed. See above.")
	}
}

// cmdVersion implements `aseprite-builder version`.
func (c *CLI) cmdVersion() int {
	fmt.Fprintf(c.Stdout, "aseprite-builder %s\n", c.BuilderVersion)
	svc, err := newService(context.Background(), c.BuilderVersion, false)
	if err != nil {
		return 0 // still exit 0: builder version printed
	}
	if inst, ok, err2 := svc.CurrentInstall(); err2 == nil && ok {
		fmt.Fprintf(c.Stdout, "installed Aseprite: %s (%s)\n", inst.AsepriteVersion, inst.AppPath)
	} else {
		fmt.Fprintln(c.Stdout, "installed Aseprite: none")
	}
	if latest, err2 := svc.LatestStable(context.Background()); err2 == nil {
		fmt.Fprintf(c.Stdout, "latest stable Aseprite: %s\n", latest)
	} else {
		fmt.Fprintln(c.Stdout, "latest stable Aseprite: unknown (check your network)")
	}
	return 0
}
