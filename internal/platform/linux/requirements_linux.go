//go:build linux

package linux

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/packages/apt"
	"github.com/akalp/aseprite-builder/internal/platform"
)

// manager selects the distribution package backend for rel. Phase 3
// supports Debian and Ubuntu through apt; other distributions fail
// safely (nil, "") and the requirement check explains that support is
// planned. Phase 4 extends this selection.
func (p Platform) manager(rel packages.OSRelease) (packages.PackageManager, string) {
	m := apt.Platform{Runner: p.Runner, OSRelease: rel}
	if !m.Applies() {
		return nil, ""
	}
	return m, "apt"
}

// selectedManager reads os-release and selects the backend.
func (p Platform) selectedManager() (packages.PackageManager, string, error) {
	rel, err := p.osRelease()
	if err != nil {
		return nil, "", err
	}
	mgr, name := p.manager(rel)
	return mgr, name, nil
}

// managerInstallCommand renders the copy-paste command for pkgs under
// the selected backend. Phase 4 extends this alongside the manager
// set; the instruction and the consented run come from the same
// manager implementation so they cannot drift.
func managerInstallCommand(manager string, pkgs []packages.Package) string {
	if manager == "apt" {
		return apt.InstallCommand(pkgs)
	}
	return ""
}

// Requirements checks the distribution, the system-managed build
// packages and free disk space. It never installs anything: missing
// packages come with the exact command the user can run (spec §16),
// which both the frontends and the consent pre-flight consume.
func (p Platform) Requirements(ctx context.Context) ([]platform.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var checks []platform.Check

	rel, relErr := p.osRelease()
	mgr, managerName := p.manager(rel)

	// 1. Distribution / package manager family.
	switch {
	case relErr != nil:
		checks = append(checks, platform.Check{
			Name:   "Distribution",
			OK:     false,
			Detail: "cannot read " + p.osReleasePath(),
			Fix:    "This release supports Debian and Ubuntu on Linux; ensure /etc/os-release is readable",
		})
	case mgr == nil:
		checks = append(checks, platform.Check{
			Name:   "Distribution",
			OK:     false,
			Detail: rel.Display(),
			Fix: "This release of aseprite-builder supports Debian and Ubuntu on Linux; " +
				"support for other distributions is planned.",
		})
	default:
		checks = append(checks, platform.Check{
			Name:   "Distribution",
			OK:     true,
			Detail: rel.Display(),
			Source: "system",
		})
	}

	// 2. System build packages (distribution-managed, spec §16).
	if mgr != nil {
		required := apt.RequiredPackages()
		label := "Build packages (" + managerName + ")"
		missing, err := mgr.MissingPackages(ctx, required)
		switch {
		case err != nil:
			checks = append(checks, platform.Check{
				Name:   label,
				OK:     false,
				Detail: err.Error(),
				Fix:    "The distribution package database could not be read; run the doctor for details",
			})
		case len(missing) == 0:
			checks = append(checks, platform.Check{
				Name:   label,
				OK:     true,
				Detail: fmt.Sprintf("all %d required packages installed", len(required)),
				Source: "system",
			})
		default:
			checks = append(checks, platform.Check{
				Name:   label,
				OK:     false,
				Detail: "missing: " + strings.Join(packages.Names(missing), ", "),
				Fix:    managerInstallCommand(managerName, missing),
			})
		}
	}

	// 3. Disk space (non-fatal warning level).
	free, err := freeDiskBytes(p.InstallRoot)
	switch {
	case err != nil:
		checks = append(checks, platform.Check{
			Name: "Disk space",
			OK:   false,
			Fix:  "Check free space on your home filesystem",
		})
	case free < minFreeDiskBytes:
		checks = append(checks, platform.Check{
			Name:   "Disk space",
			OK:     false,
			Detail: fmt.Sprintf("%.1f GiB free, at least 10 GiB needed", float64(free)/(1<<30)),
		})
	default:
		checks = append(checks, platform.Check{
			Name:   "Disk space",
			OK:     true,
			Detail: fmt.Sprintf("%.1f GiB free", float64(free)/(1<<30)),
		})
	}

	return checks, nil
}
