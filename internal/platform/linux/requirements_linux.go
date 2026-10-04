//go:build linux

package linux

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/platform"
)

// Requirements checks the distribution, the system-managed build
// packages and free disk space. It never installs anything: missing
// packages come with the exact command the user can run (spec §16),
// which both the frontends and the consent pre-flight consume.
func (p Platform) Requirements(ctx context.Context) ([]platform.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var checks []platform.Check

	rel, relErr := p.osRelease()
	var mgr packages.Manager
	var managerName string
	if relErr == nil {
		var err error
		mgr, managerName, err = p.selectManager(ctx, rel)
		if err != nil {
			return nil, err
		}
	}

	// 1. Distribution / package manager family.
	switch {
	case relErr != nil:
		checks = append(checks, platform.Check{
			Name:   "Distribution",
			OK:     false,
			Detail: "cannot read " + p.osReleasePath(),
			Fix:    "Ensure " + p.osReleasePath() + " is readable",
		})
	case mgr == nil:
		checks = append(checks, platform.Check{
			Name:   "Distribution",
			OK:     false,
			Detail: rel.Display(),
			Fix:    supportedDistrosFix,
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
		required := mgr.RequiredPackages()
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
				Fix:    mgr.InstallCommand(missing),
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
