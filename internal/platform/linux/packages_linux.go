//go:build linux

package linux

import (
	"context"
	"fmt"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/packages/apt"
)

// SystemPackages implements app.SystemPackageProvider: it reports the
// missing distribution packages and the exact command to install them.
// It is read-only and never elevates privileges.
func (p Platform) SystemPackages(ctx context.Context) (packages.Report, error) {
	mgr, name, err := p.selectedManager()
	if err != nil {
		return packages.Report{}, err
	}
	if mgr == nil {
		// Unsupported distribution: Requirements explains it; no
		// package command is offered.
		return packages.Report{}, nil
	}
	missing, err := mgr.MissingPackages(ctx, apt.RequiredPackages())
	if err != nil {
		return packages.Report{}, fmt.Errorf("check required packages: %w", err)
	}
	report := packages.Report{Manager: name, Missing: missing}
	if len(missing) > 0 {
		report.Command = managerInstallCommand(name, missing)
	}
	return report, nil
}

// InstallSystemPackages implements app.SystemPackageInstaller: it runs
// the distribution's install command through the manager, visibly and
// with the caller's terminal attached. The caller must have explicit
// user consent first (spec §16); this method never prompts.
func (p Platform) InstallSystemPackages(ctx context.Context, pkgs []packages.Package) error {
	mgr, _, err := p.selectedManager()
	if err != nil {
		return err
	}
	if mgr == nil {
		return packages.ErrUnsupportedDistro
	}
	return mgr.Install(ctx, pkgs)
}
