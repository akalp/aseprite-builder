// Package apt implements the Debian/Ubuntu package backend.
//
// It talks to the distribution's own tools only:
//
//   - package presence is read from dpkg's status database with
//     dpkg-query — never guessed, never version-compared;
//   - installation runs the same `sudo apt-get install …` command the
//     user would run, visibly and interactively, and only when the
//     caller has already obtained explicit consent (spec §16).
//
// The required-package table below is builder platform knowledge — the
// sanctioned names-not-versions exception (spec §2/§5) — and mirrors
// the Ubuntu/Debian dependency command upstream documents in its
// INSTALL.md. A new upstream release that merely bumps versions does
// not change this table.
package apt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// Default system tool locations. Tests and unusual systems override
// the Platform fields.
const (
	DefaultAPTGet    = "/usr/bin/apt-get"
	DefaultDpkgQuery = "/usr/bin/dpkg-query"
)

// requiredPackages mirrors upstream's documented Ubuntu/Debian
// dependency command (INSTALL.md):
//
//	sudo apt-get install -y g++ clang cmake ninja-build unzip \
//	    libx11-dev libxcursor-dev libxi-dev libxrandr-dev \
//	    libgl1-mesa-dev libfontconfig1-dev
//
// Names only — the distribution decides the versions. `unzip` is kept
// for parity with the documented command (upstream's build script uses
// it when it downloads and extracts Skia itself). libcurl is NOT in the
// list: upstream vendors curl under third_party/curl for the default
// build (USE_SHARED_CURL=off), so only a shared-curl build would need
// libcurl4-openssl-dev.
func requiredPackages() []packages.Package {
	return []packages.Package{
		{Name: "g++", What: "C++ compiler"},
		// clang is part of upstream's documented dependency command;
		// the prebuilt Skia builds link against libstdc++ with it.
		{Name: "clang", What: "C/C++ compiler"},
		{Name: "cmake", What: "build system"},
		{Name: "ninja-build", What: "build backend"},
		{Name: "unzip", What: "archive extraction (upstream build fallback)"},
		{Name: "libx11-dev", What: "X11 client library (dev)"},
		{Name: "libxcursor-dev", What: "X11 cursor library (dev)"},
		{Name: "libxi-dev", What: "X11 input extension (dev)"},
		{Name: "libxrandr-dev", What: "X11 resize/rotation extension (dev)"},
		{Name: "libgl1-mesa-dev", What: "OpenGL (dev)"},
		{Name: "libfontconfig1-dev", What: "font configuration (dev)"},
	}
}

// RequiredPackages returns the Debian/Ubuntu packages the builder
// expects for a full Linux build.
func RequiredPackages() []packages.Package { return requiredPackages() }

// RequiredPackages implements packages.Manager.
func (p Platform) RequiredPackages() []packages.Package { return RequiredPackages() }

// Platform implements packages.Manager for apt.
type Platform struct {
	// Runner executes probe and install commands (injected for tests).
	Runner runner.Runner
	// APTGet is the apt-get executable ("" → DefaultAPTGet).
	APTGet string
	// DpkgQuery is the dpkg-query executable ("" → DefaultDpkgQuery).
	DpkgQuery string
}

var _ packages.Manager = Platform{}

// Detect implements packages.PackageManager: apt is usable when both
// its install tool and the dpkg query tool are present. Family
// applicability (Debian/Ubuntu) is decided by
// internal/packages/selector.
func (p Platform) Detect(context.Context) bool {
	return packages.ToolExists(p.aptGet()) && packages.ToolExists(p.dpkgQuery())
}

// MissingPackages implements packages.PackageManager by reading
// dpkg's status database. A non-zero dpkg-query exit is expected when
// some packages have no record at all (never installed): their status
// lines are simply absent and are reported as missing.
func (p Platform) MissingPackages(ctx context.Context, pkgs []packages.Package) ([]packages.Package, error) {
	if len(pkgs) == 0 {
		return nil, nil
	}
	args := append([]string{"-W", "-f=" + dpkgStatusFormat}, packages.Names(pkgs)...)
	res, err := p.Runner.Run(ctx, runner.Command{Name: p.dpkgQuery(), Args: args})
	var exitErr runner.ErrExit
	if err != nil && !errors.As(err, &exitErr) {
		return nil, fmt.Errorf("probe installed packages with dpkg-query: %w", err)
	}
	statuses := parseDpkgStatuses(res.Stdout)
	var missing []packages.Package
	for _, pkg := range pkgs {
		if statuses[pkg.Name] != "installed" {
			missing = append(missing, pkg)
		}
	}
	return missing, nil
}

// dpkgStatusFormat asks dpkg for the binary package name and its
// status-description word ("installed", "not-installed", "config-files", …).
const dpkgStatusFormat = "${binary:Package}\t${db:Status-Status}\n"

// parseDpkgStatuses maps package name → status word.
func parseDpkgStatuses(out string) map[string]string {
	statuses := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, status, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		name, status = strings.TrimSpace(name), strings.TrimSpace(status)
		if name == "" {
			continue
		}
		statuses[name] = status
	}
	return statuses
}

// installArgs is the exact argument vector executed through sudo, and
// the source of the user-facing command text.
func installArgs(pkgs []packages.Package) []string {
	return append([]string{"apt-get", "install"}, packages.Names(pkgs)...)
}

// InstallCommand renders the exact command a user can copy-paste to
// install pkgs. Install executes the same command, so the instruction
// and the consented run cannot drift.
func InstallCommand(pkgs []packages.Package) string {
	if len(pkgs) == 0 {
		return ""
	}
	return "sudo " + strings.Join(installArgs(pkgs), " ")
}

// InstallCommand implements packages.Manager.
func (p Platform) InstallCommand(pkgs []packages.Package) string { return InstallCommand(pkgs) }

// Install runs the visible, interactive apt-get install for pkgs.
//
// It must only be called after explicit user consent. The command runs
// with the caller's terminal attached (runner.Command.Interactive):
// the user watches it happen and can answer apt's own confirmation.
// There is deliberately no `--yes` and no output capture: nothing
// about a privileged operation is hidden (spec §16).
func (p Platform) Install(ctx context.Context, pkgs []packages.Package) error {
	if len(pkgs) == 0 {
		return nil
	}
	return packages.RunSudo(ctx, p.Runner, installArgs(pkgs))
}

func (p Platform) aptGet() string {
	if p.APTGet != "" {
		return p.APTGet
	}
	return DefaultAPTGet
}

func (p Platform) dpkgQuery() string {
	if p.DpkgQuery != "" {
		return p.DpkgQuery
	}
	return DefaultDpkgQuery
}
