// Package zypper implements the openSUSE/SUSE package backend.
//
// It talks to the distribution's own tools only:
//
//   - package presence is read from the local RPM database with
//     `rpm -q` — never guessed, never version-compared;
//   - installation runs the same `sudo zypper install …` command the
//     user would run, visibly and interactively, and only when the
//     caller has already obtained explicit consent (spec §16).
//
// The required-package table below is builder platform knowledge — the
// sanctioned names-not-versions exception (spec §2/§5). Upstream
// documents the Ubuntu/Debian command in INSTALL.md; the openSUSE names
// are the distribution's own conventions for the same logical
// dependencies. A new upstream release that merely bumps versions does
// not change this table.
package zypper

import (
	"context"
	"strings"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// Default tool locations. Tests and unusual systems override the
// Platform fields.
const (
	DefaultZypper = "/usr/bin/zypper"
	DefaultRPM    = "/usr/bin/rpm"
)

// requiredPackages is the openSUSE/SUSE package table for the logical
// dependencies upstream's Debian list documents. Names only — the
// distribution decides the versions.
func requiredPackages() []packages.Package {
	return []packages.Package{
		{Name: "gcc-c++", What: "C++ compiler"},
		{Name: "clang", What: "C/C++ compiler"},
		{Name: "cmake", What: "build system"},
		{Name: "ninja", What: "build backend"},
		{Name: "unzip", What: "archive extraction (upstream build fallback)"},
		{Name: "libX11-devel", What: "X11 client library (dev)"},
		{Name: "libXcursor-devel", What: "X11 cursor library (dev)"},
		{Name: "libXi-devel", What: "X11 input extension (dev)"},
		{Name: "libXrandr-devel", What: "X11 resize/rotation extension (dev)"},
		{Name: "Mesa-libGL-devel", What: "OpenGL (dev)"},
		{Name: "fontconfig-devel", What: "font configuration (dev)"},
	}
}

// RequiredPackages returns the openSUSE/SUSE packages the builder
// expects for a full Linux build.
func RequiredPackages() []packages.Package { return requiredPackages() }

// Platform implements packages.Manager for zypper.
type Platform struct {
	// Runner executes probe and install commands (injected for tests).
	Runner runner.Runner
	// Zypper is the zypper executable ("" → DefaultZypper).
	Zypper string
	// RPM is the rpm query executable ("" → DefaultRPM).
	RPM string
}

var _ packages.Manager = Platform{}

// Detect implements packages.PackageManager: zypper is usable when
// both its install tool and the rpm query tool are present. Family
// applicability (openSUSE/SUSE) is decided by
// internal/packages/selector.
func (p Platform) Detect(context.Context) bool {
	return packages.ToolExists(p.zypper()) && packages.ToolExists(p.rpm())
}

// RequiredPackages implements packages.Manager.
func (p Platform) RequiredPackages() []packages.Package { return RequiredPackages() }

// MissingPackages implements packages.PackageManager by querying the
// local RPM database. rpm reports absence with a non-zero exit status,
// so one query per package is enough.
func (p Platform) MissingPackages(ctx context.Context, pkgs []packages.Package) ([]packages.Package, error) {
	return packages.MissingByExitStatus(ctx, p.Runner, pkgs, func(name string) (string, []string) {
		return p.rpm(), []string{"-q", name}
	})
}

// installArgs is the exact argument vector executed through sudo, and
// the source of the user-facing command text.
func installArgs(pkgs []packages.Package) []string {
	return append([]string{"zypper", "install"}, packages.Names(pkgs)...)
}

// InstallCommand renders the exact command a user can copy-paste.
func InstallCommand(pkgs []packages.Package) string {
	if len(pkgs) == 0 {
		return ""
	}
	return "sudo " + strings.Join(installArgs(pkgs), " ")
}

// InstallCommand implements packages.Manager.
func (p Platform) InstallCommand(pkgs []packages.Package) string { return InstallCommand(pkgs) }

// Install runs the visible, interactive zypper install for pkgs. It
// must only be called after explicit user consent (spec §16); zypper's
// own confirmation stays visible.
func (p Platform) Install(ctx context.Context, pkgs []packages.Package) error {
	if len(pkgs) == 0 {
		return nil
	}
	return packages.RunSudo(ctx, p.Runner, installArgs(pkgs))
}

func (p Platform) zypper() string {
	if p.Zypper != "" {
		return p.Zypper
	}
	return DefaultZypper
}

func (p Platform) rpm() string {
	if p.RPM != "" {
		return p.RPM
	}
	return DefaultRPM
}
