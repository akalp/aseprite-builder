// Package platform defines the OS abstraction used by the application
// services. Concrete implementations live in platform/darwin,
// platform/windows and platform/linux; application code never calls
// runtime.GOOS directly.
package platform

import (
	"context"
	"errors"
	"fmt"
	"runtime"
)

// SystemInfo describes the host operating system.
type SystemInfo struct {
	// OS is a canonical name: "darwin", "windows", "linux".
	OS string `json:"os"`
	// OSVersion is the user-facing OS version ("15.2") when discoverable.
	OSVersion string `json:"os_version,omitempty"`
	// Arch is the machine architecture ("arm64", "amd64").
	Arch string `json:"arch"`
	// Supported reports whether this OS/arch combination is a supported
	// build target for aseprite-builder.
	Supported bool `json:"supported"`
}

// Check is the result of evaluating one system-managed requirement.
// Kind classifies the check for reporting; OK=false carries a Fix
// hint for the user.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
	// Source records where a tool came from: "system", "builder cache",
	// or "" for environmental checks.
	Source string `json:"source,omitempty"`
}

// BuildOutput is the artifact a completed build hands to the platform
// installer. On macOS the build produces a .app bundle; other platforms
// populate Executable/BinDir/Data instead.
type BuildOutput struct {
	Version    string // Aseprite version string, e.g. "1.3.18.6"
	AppPath    string // macOS: path to Aseprite.app produced by the build
	Executable string // plain executable path (non-macOS or bundle exec)
	// BinDir is the directory holding the executable plus everything
	// needed to run it (data dir, DLLs). Non-bundle installers
	// activate this directory as the payload. Empty for bundle
	// layouts. DataDir is the runtime data directory it contains.
	BinDir  string
	DataDir string
	// IconsDir is the upstream source's icon set directory
	// (<source>/data/icons on macOS), used to generate the native .icns
	// bundle icons. Empty when unavailable.
	IconsDir string
	// DesktopFile is the upstream Linux .desktop entry
	// (<source>/src/desktop/linux/aseprite.desktop), used as the
	// template for the user-level desktop entry. Empty when
	// unavailable (the Linux installer falls back to a built-in
	// entry).
	DesktopFile string
}

// InstallOutcome describes what the platform installer did.
type InstallOutcome struct {
	AppPath        string // activated installation path
	PreviousPath   string // retained previous installation, "" when none
	StagingRemoved bool
}

// UninstallOptions selects what an uninstall may remove.
type UninstallOptions struct {
	// Force removes the application even when the builder cannot prove
	// it owns the bundle. Never used implicitly.
	Force bool
	// RemovePrevious also deletes retained previous versions.
	RemovePrevious bool
}

// ErrNotOwned is returned when an uninstall is attempted on an
// application the builder cannot prove it owns.
var ErrNotOwned = errors.New("application bundle is not managed by aseprite-builder")

// Platform is the contract each operating system implements. Methods
// map to the application service operations; the app layer never
// special-cases an OS.
type Platform interface {
	// Detect reports the OS, version and architecture.
	Detect(ctx context.Context) (SystemInfo, error)
	// Requirements evaluates system-managed toolchain requirements
	// (compilers, SDKs) that the builder must never install itself.
	Requirements(ctx context.Context) ([]Check, error)
	// Prepare performs any platform setup before a build starts.
	Prepare(ctx context.Context) error
	// Install activates a verified build output as the native
	// application installation, transactionally (never destroying the
	// current installation before the replacement is verified).
	Install(ctx context.Context, out BuildOutput) (InstallOutcome, error)
	// Launch starts the installed application.
	Launch(ctx context.Context) error
	// Uninstall removes the builder-owned application.
	Uninstall(ctx context.Context, opts UninstallOptions) error
	// InstalledVersion reports the version recorded in the installed
	// bundle, or "" when no installation exists.
	InstalledVersion(ctx context.Context) (string, error)
	// DefaultInstallPath is where the native installation lives
	// (~/Applications/Aseprite.app on macOS).
	DefaultInstallPath() string
}

// InstalledExecutableProvider is an optional Platform capability for
// layouts that are not a macOS bundle (a plain installation
// directory): the absolute path of the installed executable, so the
// service layer can verify/repair installations without knowing the
// per-OS shape. Implementations may return the path even when the
// executable is currently missing — callers stat it.
type InstalledExecutableProvider interface {
	InstalledExecutable() (string, error)
}

// CurrentArch maps runtime architecture to the builder's canonical
// naming (amd64 → amd64 etc.). Only a convenience for Detect
// implementations.
func CurrentArch() string { return runtime.GOARCH }

// CanonicalOS maps runtime.GOOS to the builder's canonical OS names.
func CanonicalOS() string { return runtime.GOOS }

// SupportedTargets lists the platform/arch pairs the builder supports
// today. Extending this table is a deliberate product decision, not a
// runtime guess.
func SupportedTargets() map[string][]string {
	return map[string][]string{
		"darwin":  {"arm64", "amd64"},
		"windows": {"amd64"},
		"linux":   {"amd64"},
	}
}

// IsSupportedTarget reports whether os/arch is a supported target.
func IsSupportedTarget(osName, arch string) bool {
	arches, ok := SupportedTargets()[osName]
	if !ok {
		return false
	}
	for _, a := range arches {
		if a == arch {
			return true
		}
	}
	return false
}

// String renders a Check for logs and reports.
func (c Check) String() string {
	status := "✓"
	if !c.OK {
		status = "✗"
	}
	out := fmt.Sprintf("%s %s", status, c.Name)
	if c.Detail != "" {
		out += ": " + c.Detail
	}
	return out
}
