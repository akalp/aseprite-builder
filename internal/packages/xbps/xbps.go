// Package xbps implements the Void Linux package backend.
//
// It talks to the distribution's own tools only:
//
//   - package presence is read from the local package database with
//     `xbps-query <pkg>` — never guessed, never version-compared;
//   - installation runs the same `sudo xbps-install -S …` command the
//     user would run, visibly and interactively, and only when the
//     caller has already obtained explicit consent (spec §16).
//
// The required-package table below is builder platform knowledge — the
// sanctioned names-not-versions exception (spec §2/§5). Upstream
// documents the Ubuntu/Debian command in INSTALL.md; the Void names are
// the distribution's own conventions for the same logical dependencies.
// A new upstream release that merely bumps versions does not change
// this table.
package xbps

import (
	"context"
	"strings"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// Default tool locations. Tests and unusual systems override the
// Platform fields.
const (
	DefaultXbpsInstall = "/usr/bin/xbps-install"
	DefaultXbpsQuery   = "/usr/bin/xbps-query"
)

// requiredPackages is the Void package table for the logical
// dependencies upstream's Debian list documents. Names only — the
// distribution decides the versions.
func requiredPackages() []packages.Package {
	return []packages.Package{
		{Name: "gcc", What: "C++ compiler"},
		{Name: "clang", What: "C/C++ compiler"},
		{Name: "cmake", What: "build system"},
		{Name: "ninja", What: "build backend"},
		{Name: "unzip", What: "archive extraction (upstream build fallback)"},
		{Name: "libX11-devel", What: "X11 client library (dev)"},
		{Name: "libXcursor-devel", What: "X11 cursor library (dev)"},
		{Name: "libXi-devel", What: "X11 input extension (dev)"},
		{Name: "libXrandr-devel", What: "X11 resize/rotation extension (dev)"},
		{Name: "MesaLib-devel", What: "OpenGL (dev)"},
		{Name: "fontconfig-devel", What: "font configuration (dev)"},
	}
}

// RequiredPackages returns the Void packages the builder expects for a
// full Linux build.
func RequiredPackages() []packages.Package { return requiredPackages() }

// Platform implements packages.Manager for xbps.
type Platform struct {
	// Runner executes probe and install commands (injected for tests).
	Runner runner.Runner
	// XbpsInstall is the xbps-install executable ("" → DefaultXbpsInstall).
	XbpsInstall string
	// XbpsQuery is the xbps-query executable ("" → DefaultXbpsQuery).
	XbpsQuery string
}

var _ packages.Manager = Platform{}

// Detect implements packages.PackageManager: xbps is usable when both
// its install tool and its query tool are present. Family
// applicability (Void) is decided by internal/packages/selector.
func (p Platform) Detect(context.Context) bool {
	return packages.ToolExists(p.xbpsInstall()) && packages.ToolExists(p.xbpsQuery())
}

// RequiredPackages implements packages.Manager.
func (p Platform) RequiredPackages() []packages.Package { return RequiredPackages() }

// MissingPackages implements packages.PackageManager by querying the
// installed package database. xbps-query reports absence with a
// non-zero exit status, so one query per package is enough.
func (p Platform) MissingPackages(ctx context.Context, pkgs []packages.Package) ([]packages.Package, error) {
	return packages.MissingByExitStatus(ctx, p.Runner, pkgs, func(name string) (string, []string) {
		return p.xbpsQuery(), []string{name}
	})
}

// installArgs is the exact argument vector executed through sudo, and
// the source of the user-facing command text. -S refreshes repository
// data; it never skips a confirmation.
func installArgs(pkgs []packages.Package) []string {
	return append([]string{"xbps-install", "-S"}, packages.Names(pkgs)...)
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

// Install runs the visible, interactive xbps-install for pkgs. It must
// only be called after explicit user consent (spec §16); xbps's own
// confirmation stays visible.
func (p Platform) Install(ctx context.Context, pkgs []packages.Package) error {
	if len(pkgs) == 0 {
		return nil
	}
	return packages.RunSudo(ctx, p.Runner, installArgs(pkgs))
}

func (p Platform) xbpsInstall() string {
	if p.XbpsInstall != "" {
		return p.XbpsInstall
	}
	return DefaultXbpsInstall
}

func (p Platform) xbpsQuery() string {
	if p.XbpsQuery != "" {
		return p.XbpsQuery
	}
	return DefaultXbpsQuery
}
