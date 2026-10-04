// Package pacman implements the Arch Linux package backend.
//
// It talks to the distribution's own tools only:
//
//   - package presence is read from the local package database with
//     `pacman -Qi` — never guessed, never version-compared;
//   - installation runs the same `sudo pacman -S --needed …` command
//     the user would run, visibly and interactively, and only when
//     the caller has already obtained explicit consent (spec §16).
//
// The required-package table below is builder platform knowledge — the
// sanctioned names-not-versions exception (spec §2/§5). Upstream
// documents the Ubuntu/Debian command in INSTALL.md; the Arch names are
// the distribution's own conventions for the same logical dependencies.
// A new upstream release that merely bumps versions does not change
// this table.
package pacman

import (
	"context"
	"strings"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// Default tool location. Tests and unusual systems override the
// Platform fields.
const DefaultPacman = "/usr/bin/pacman"

// requiredPackages is the Arch package table for the logical
// dependencies upstream's Debian list documents. Arch ships headers
// with the library packages (no -dev suffix); libglvnd provides the
// OpenGL headers and dispatch library. Names only — the distribution
// decides the versions.
func requiredPackages() []packages.Package {
	return []packages.Package{
		{Name: "gcc", What: "C++ compiler"},
		{Name: "clang", What: "C/C++ compiler"},
		{Name: "cmake", What: "build system"},
		{Name: "ninja", What: "build backend"},
		{Name: "unzip", What: "archive extraction (upstream build fallback)"},
		{Name: "libx11", What: "X11 client library (dev)"},
		{Name: "libxcursor", What: "X11 cursor library (dev)"},
		{Name: "libxi", What: "X11 input extension (dev)"},
		{Name: "libxrandr", What: "X11 resize/rotation extension (dev)"},
		{Name: "libglvnd", What: "OpenGL (dev)"},
		{Name: "fontconfig", What: "font configuration (dev)"},
	}
}

// RequiredPackages returns the Arch packages the builder expects for a
// full Linux build.
func RequiredPackages() []packages.Package { return requiredPackages() }

// Platform implements packages.Manager for pacman.
type Platform struct {
	// Runner executes probe and install commands (injected for tests).
	Runner runner.Runner
	// Pacman is the pacman executable ("" → DefaultPacman).
	Pacman string
}

var _ packages.Manager = Platform{}

// Detect implements packages.PackageManager: pacman is usable when its
// binary is present. Family applicability (Arch) is decided by
// internal/packages/selector.
func (p Platform) Detect(context.Context) bool {
	return packages.ToolExists(p.pacman())
}

// RequiredPackages implements packages.Manager.
func (p Platform) RequiredPackages() []packages.Package { return RequiredPackages() }

// MissingPackages implements packages.PackageManager by querying the
// local package database. `pacman -Qi` exits non-zero for packages
// that are not installed, so one query per package is enough.
func (p Platform) MissingPackages(ctx context.Context, pkgs []packages.Package) ([]packages.Package, error) {
	return packages.MissingByExitStatus(ctx, p.Runner, pkgs, func(name string) (string, []string) {
		return p.pacman(), []string{"-Qi", name}
	})
}

// installArgs is the exact argument vector executed through sudo, and
// the source of the user-facing command text. --needed skips packages
// that are already up to date; it never skips a confirmation.
func installArgs(pkgs []packages.Package) []string {
	return append([]string{"pacman", "-S", "--needed"}, packages.Names(pkgs)...)
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

// Install runs the visible, interactive pacman install for pkgs. It
// must only be called after explicit user consent (spec §16); pacman's
// own confirmation stays visible.
func (p Platform) Install(ctx context.Context, pkgs []packages.Package) error {
	if len(pkgs) == 0 {
		return nil
	}
	return packages.RunSudo(ctx, p.Runner, installArgs(pkgs))
}

func (p Platform) pacman() string {
	if p.Pacman != "" {
		return p.Pacman
	}
	return DefaultPacman
}
