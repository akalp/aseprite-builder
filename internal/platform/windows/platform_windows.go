//go:build windows

// Package windows implements platform.Platform for Windows x64. It
// manages only what Windows requires of aseprite-builder: detecting
// the system-managed Microsoft toolchain (Visual Studio / Build Tools,
// the C++ workload, the Windows SDK), capturing its environment for
// the build, installing a user-local Aseprite directory transactionally
// (%LOCALAPPDATA%\Programs\Aseprite), launching, updating and
// uninstalling builder-owned installations.
//
// Visual Studio, the Windows SDK and the compiler are system-managed:
// this package detects them and reports remediation hints, but never
// installs or bundles them. No administrator elevation is required or
// attempted (spec §21).
package windows

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// minFreeDiskBytes mirrors the darwin guard: Skia (~1 GiB), the source
// archive (~800 MiB), extracted trees (~3 GiB), the build (~2 GiB) and
// the installation (~700 MiB) all share the user's disk.
const minFreeDiskBytes = 10 << 30

// markerName is the ownership marker installed next to the installed
// aseprite.exe. Uninstall refuses to touch installations without it
// (non-bundle layout: the marker lives next to the executable).
const markerName = ".aseprite-builder-managed"

// retentionDirName holds staging and the previous installation between
// updates. Only builder-owned installations are stored or removed
// here.
const retentionDirName = ".aseprite-builder"

// installDirName is the directory created under
// %LOCALAPPDATA%\Programs (spec §21).
const installDirName = "Aseprite"

// exeName is the executable the upstream Windows build produces.
const exeName = "aseprite.exe"

// Platform implements platform.Platform for Windows.
type Platform struct {
	// Runner executes external commands (injected for tests).
	Runner runner.Runner
	// ProgramsRoot is the user-local install root
	// (%LOCALAPPDATA%\Programs).
	ProgramsRoot string
	// StartMenuRoot is the user's Start Menu programs folder
	// (%APPDATA%\Microsoft\Windows\Start Menu\Programs); empty
	// disables shortcut creation (used by tests).
	StartMenuRoot string
	// BuilderVersion recorded in the ownership marker.
	BuilderVersion string
	// AllowReplaceUnowned lets install overwrite an existing Aseprite
	// directory the builder cannot prove it owns. The user must opt in
	// explicitly.
	AllowReplaceUnowned bool
	// Logger receives non-fatal operational notes (nil disables).
	Logger func(format string, args ...any)
	// Now overrides wall-clock time in tests.
	Now func() time.Time
}

// DefaultProgramsRoot resolves %LOCALAPPDATA%\Programs.
func DefaultProgramsRoot() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("resolve install dir: LOCALAPPDATA is not set")
	}
	return filepath.Join(local, "Programs"), nil
}

// DefaultStartMenuRoot resolves the user's Start Menu programs folder.
func DefaultStartMenuRoot() string {
	return filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs")
}

// New returns a Windows Platform with the default install roots.
func New(r runner.Runner, builderVersion string) (Platform, error) {
	root, err := DefaultProgramsRoot()
	if err != nil {
		return Platform{}, err
	}
	return Platform{
		Runner:         r,
		ProgramsRoot:   root,
		StartMenuRoot:  DefaultStartMenuRoot(),
		BuilderVersion: builderVersion,
		Now:            time.Now,
	}, nil
}

// SetAllowReplaceUnowned implements the optional consent interface the
// app service uses when the user explicitly authorizes replacing an
// existing installation the builder cannot prove it owns.
func (p *Platform) SetAllowReplaceUnowned(v bool) { p.AllowReplaceUnowned = v }

// note logs a non-fatal operational note.
func (p Platform) note(format string, args ...any) {
	if p.Logger != nil {
		p.Logger(format, args...)
	}
}

// DefaultInstallPath is %LOCALAPPDATA%\Programs\Aseprite.
func (p Platform) DefaultInstallPath() string {
	return filepath.Join(p.ProgramsRoot, installDirName)
}

// exePath is where the installed executable lives.
func (p Platform) exePath() string {
	return filepath.Join(p.DefaultInstallPath(), exeName)
}

type marker struct {
	Version        string    `json:"aseprite_version"`
	BuilderVersion string    `json:"builder_version"`
	InstalledAt    time.Time `json:"installed_at"`
}

// Detect reports the Windows version and architecture.
func (p Platform) Detect(_ context.Context) (platform.SystemInfo, error) {
	info := platform.SystemInfo{
		OS:        "windows",
		Arch:      platform.CurrentArch(),
		Supported: platform.IsSupportedTarget("windows", platform.CurrentArch()),
	}
	res, err := p.Runner.Run(context.Background(), runner.Command{
		Name: "cmd", Args: []string{"/c", "ver"},
	})
	if err == nil {
		info.OSVersion = parseWindowsVersion(res.Stdout)
	}
	return info, nil
}

// parseWindowsVersion extracts "10.0.26100" from cmd's banner
// ("Microsoft Windows [Version 10.0.26100.3476]"). Unparseable output
// yields "" (detection stays non-fatal).
func parseWindowsVersion(verOut string) string {
	start := strings.Index(verOut, "[")
	end := strings.Index(verOut, "]")
	if start < 0 || end <= start {
		return ""
	}
	full := strings.TrimSpace(verOut[start+1 : end])
	full = strings.TrimPrefix(full, "Version ")
	// Report major.minor.build (no trailing superseding build number).
	if i := strings.LastIndexByte(full, '.'); i > 0 {
		return full[:i]
	}
	return full
}
