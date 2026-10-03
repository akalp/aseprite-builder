package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/akalp/aseprite-builder/internal/app"
)

// cmdUpdate implements `aseprite-builder update`.
func (c *CLI) cmdUpdate(ctx context.Context, args []string) int {
	fs, v := updateFlags()
	if code, ok := parseArgs("update", fs, args, c.Stdout, c.Stderr); !ok {
		return code
	}

	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	if !*v.Yes && !c.confirm("Check for updates and install the latest stable Aseprite?") {
		fmt.Fprintln(c.Stderr, "update cancelled; nothing was changed")
		return int(exitCode(1))
	}

	hud := c.newHUD(false)
	result, err := svc.Update(ctx, app.InstallOptions{LaunchAfterInstall: *v.Launch}, hud.Sink())
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
func (c *CLI) cmdLaunch(ctx context.Context, args []string) int {
	fs := plainFlags("launch")
	if code, ok := parseArgs("launch", fs, args, c.Stdout, c.Stderr); !ok {
		return code
	}
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
func (c *CLI) cmdRepair(ctx context.Context, args []string) int {
	fs := plainFlags("repair")
	if code, ok := parseArgs("repair", fs, args, c.Stdout, c.Stderr); !ok {
		return code
	}
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
	fs, v := rebuildFlags()
	if code, ok := parseArgs("rebuild", fs, args, c.Stdout, c.Stderr); !ok {
		return code
	}

	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	hud := c.newHUD(false)
	result, err := svc.Rebuild(ctx, app.RebuildOptions{Clean: *v.Clean}, hud.Sink())
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
	fs, v := uninstallFlags()
	if code, ok := parseArgs("uninstall", fs, args, c.Stdout, c.Stderr); !ok {
		return code
	}

	svc, err := c.serviceFor(ctx, true)
	if err != nil {
		c.printServiceError(err)
		return 1
	}
	prompt := "Remove the aseprite-builder installation of Aseprite?"
	if *v.PurgeSources || *v.PurgeTools || *v.PurgeSkia || *v.PurgeLogs {
		prompt = "Remove Aseprite and the selected aseprite-builder caches?"
	}
	if !c.confirm(prompt) {
		fmt.Fprintln(c.Stderr, "uninstall cancelled; nothing was removed")
		return int(exitCode(1))
	}
	if err := svc.Uninstall(ctx, app.UninstallOptions{
		Force:          *v.Force,
		RemovePrevious: *v.RemovePrev,
	}); err != nil {
		c.printServiceError(err)
		return int(exitCode(1))
	}
	removed := []string{"the application"}
	if *v.RemovePrev {
		removed = append(removed, "the retained previous version")
	}
	for _, pr := range svc.PurgeCaches(app.PurgeOptions{
		Sources: *v.PurgeSources,
		Tools:   *v.PurgeTools,
		Skia:    *v.PurgeSkia,
		Logs:    *v.PurgeLogs,
	}) {
		if pr.Err != nil {
			fmt.Fprintf(c.Stderr, "note: could not remove %s (%s): %v\n", pr.What, pr.Dir, pr.Err)
			continue
		}
		fmt.Fprintf(c.Stderr, "   removed %s\n", pr.Dir)
	}
	fmt.Fprintf(c.Stderr, "\n✅ Removed %s.\n", strings.Join(removed, ", "))
	return 0
}

// cmdDoctor implements `aseprite-builder doctor`.
func (c *CLI) cmdDoctor(ctx context.Context, args []string) int {
	fs, v := doctorFlags()
	if code, ok := parseArgs("doctor", fs, args, c.Stdout, c.Stderr); !ok {
		return code
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
	if *v.AsJSON {
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
	// The rows come from the app layer so the TUI diagnostics screen
	// shows identical facts; this function only formats them.
	rows := r.DoctorRows()

	nameWidth := len("Installation")
	for _, row := range rows {
		if len(row.Name) > nameWidth {
			nameWidth = len(row.Name)
		}
	}

	fmt.Fprintln(c.Stdout, "Diagnostics")
	fmt.Fprintln(c.Stdout, "")
	for _, row := range rows {
		mark := "✓"
		if !row.OK {
			mark = "✗"
		}
		fmt.Fprintf(c.Stdout, "%s %-*s  %s\n", mark, nameWidth, row.Name, row.Detail)
	}
	fmt.Fprintln(c.Stdout, "")
	if r.Status == "healthy" {
		fmt.Fprintln(c.Stdout, "Everything looks good.")
	} else {
		fmt.Fprintln(c.Stdout, "Some checks failed. See above.")
	}
}

// cmdVersion implements `aseprite-builder version`.
func (c *CLI) cmdVersion(args []string) int {
	fs := plainFlags("version")
	if code, ok := parseArgs("version", fs, args, c.Stdout, c.Stderr); !ok {
		return code
	}
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
