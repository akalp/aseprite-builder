package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/akalp/aseprite-builder/internal/packages"
)

// SystemPackageProvider is implemented by platforms whose build
// requirements are distribution-managed packages (Linux). The service
// consults it through Service.SystemPackages for the CLI's
// explicit-consent path; the Requirements checks remain the
// instruction-only surface rendered by doctor and the TUI. It never
// installs anything.
type SystemPackageProvider interface {
	SystemPackages(ctx context.Context) (packages.Report, error)
}

// SystemPackageInstaller is implemented by platforms that can run the
// distribution's package installation visibly, after explicit user
// consent (spec §16).
type SystemPackageInstaller interface {
	InstallSystemPackages(ctx context.Context, pkgs []packages.Package) error
}

// SystemPackages reports the distribution packages the current
// platform requires, which of them are missing, and the exact command
// a user can run to install them. Platforms without a package backend
// return an empty report.
//
// This is the CLI pre-flight for the explicit-consent path: it is
// read-only and never elevates privileges.
func (s *Service) SystemPackages(ctx context.Context) (packages.Report, error) {
	provider, ok := s.opts.Platform.(SystemPackageProvider)
	if !ok {
		return packages.Report{}, nil
	}
	report, err := provider.SystemPackages(ctx)
	if err != nil {
		return packages.Report{}, fmt.Errorf("check system packages: %w", err)
	}
	return report, nil
}

// InstallSystemPackages runs the distribution's package installation
// for pkgs. Callers must have obtained explicit user consent first —
// this method never prompts and never elevates on its own (spec §16).
// An empty package list is a no-op.
func (s *Service) InstallSystemPackages(ctx context.Context, pkgs []packages.Package) error {
	if len(pkgs) == 0 {
		return nil
	}
	installer, ok := s.opts.Platform.(SystemPackageInstaller)
	if !ok {
		return errors.New("this platform has no system package installer")
	}
	if err := installer.InstallSystemPackages(ctx, pkgs); err != nil {
		return fmt.Errorf("install system packages: %w", err)
	}
	return nil
}
