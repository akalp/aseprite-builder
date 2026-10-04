//go:build linux

// Package linux implements platform.Platform for Linux (Debian and
// Ubuntu on amd64 in this milestone).
//
// It manages only what Linux requires of aseprite-builder: detecting
// the distribution and the system-managed toolchain, composing the
// system requirement check with the distribution's package manager
// (spec §16/§22), installing a user-local Aseprite under
// ~/.local/opt/aseprite, wiring the CLI symlink and desktop entry,
// launching, updating transactionally and uninstalling builder-owned
// files.
//
// Compilers and development libraries remain distribution-managed:
// this package detects what is missing and reports the exact command,
// but elevates privileges only after explicit user consent (the
// CLI's consent flow), never on its own.
package linux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// minFreeDiskBytes mirrors the darwin/windows guard: Skia (~1 GiB),
// the source archive (~800 MiB), extracted trees (~3 GiB), the build
// (~2 GiB) and the installation (~700 MiB) all share the user's disk.
const minFreeDiskBytes = 10 << 30

// markerName is the ownership marker installed inside the builder's
// own directory next to the executable. Uninstall refuses to touch
// installations without it.
const markerName = ".aseprite-builder-managed"

// retentionDirName holds staging and the previous installation between
// updates. Only builder-owned installations are stored or removed
// here.
const retentionDirName = ".aseprite-builder"

// installDirName is the directory created under ~/.local/opt.
const installDirName = "aseprite"

// exeName is the executable the upstream Linux build produces
// (build.sh: bin/aseprite).
const exeName = "aseprite"

// defaultOSReleasePath is the standard distribution identification
// file (spec §22).
const defaultOSReleasePath = "/etc/os-release"

// Platform implements platform.Platform for Linux.
type Platform struct {
	// Runner executes external commands (injected for tests).
	Runner runner.Runner
	// InstallRoot is the user-local install root (~/.local/opt).
	InstallRoot string
	// CLIBinDir is where the CLI symlink lives (~/.local/bin).
	CLIBinDir string
	// DataHome is the XDG data home (~/.local/share); the desktop
	// entry and icon theme are managed under it.
	DataHome string
	// OSReleasePath is the os-release file used for detection
	// (tests override it).
	OSReleasePath string
	// BuilderVersion recorded in the ownership marker.
	BuilderVersion string
	// AllowReplaceUnowned lets install overwrite an existing Aseprite
	// directory the builder cannot prove it owns. The user must opt in
	// explicitly.
	AllowReplaceUnowned bool
	// DisableRetention drops the replaced installation after the new one
	// activates instead of keeping it for rollback (advanced option,
	// spec §24/§41). The zero value retains, preserving the default.
	DisableRetention bool
	// Logger receives non-fatal operational notes (nil disables).
	Logger func(format string, args ...any)
	// Now overrides wall-clock time in tests.
	Now func() time.Time
	// managerOverride replaces the constructed package-manager set in
	// tests, pointing the managers at fabricated tool paths so backend
	// selection is deterministic on any host. nil in production (the
	// standard system locations are used).
	managerOverride map[packages.ManagerName]packages.Manager
}

// New returns a Linux Platform with the default user-local locations.
func New(r runner.Runner, builderVersion string) (Platform, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Platform{}, fmt.Errorf("resolve home dir: %w", err)
	}
	return Platform{
		Runner:         r,
		InstallRoot:    filepath.Join(home, ".local", "opt"),
		CLIBinDir:      filepath.Join(home, ".local", "bin"),
		DataHome:       dataHome(home),
		OSReleasePath:  defaultOSReleasePath,
		BuilderVersion: builderVersion,
		Now:            time.Now,
	}, nil
}

// dataHome mirrors the XDG data-home rules: $XDG_DATA_HOME when it is
// absolute, ~/.local/share otherwise.
func dataHome(home string) string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return xdg
	}
	return filepath.Join(home, ".local", "share")
}

// SetAllowReplaceUnowned implements the optional consent interface the
// app service uses when the user explicitly authorizes replacing an
// existing installation the builder cannot prove it owns.
func (p *Platform) SetAllowReplaceUnowned(v bool) { p.AllowReplaceUnowned = v }

// SetInstallRoot implements platform.InstallRootSetter: the advanced
// custom install directory (spec §41). Only an absolute path is
// accepted; the platform creates it during install.
func (p *Platform) SetInstallRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("install directory must be an absolute path: %q", root)
	}
	p.InstallRoot = root
	return nil
}

// SetRetainPrevious implements platform.RetentionSetter. Retaining the
// replaced installation is the default (spec §24).
func (p *Platform) SetRetainPrevious(retain bool) { p.DisableRetention = !retain }

// note logs a non-fatal operational note.
func (p Platform) note(format string, args ...any) {
	if p.Logger != nil {
		p.Logger(format, args...)
	}
}

// osReleasePath returns the configured os-release path.
func (p Platform) osReleasePath() string {
	if p.OSReleasePath != "" {
		return p.OSReleasePath
	}
	return defaultOSReleasePath
}

// osRelease reads and parses the distribution identification.
func (p Platform) osRelease() (packages.OSRelease, error) {
	data, err := os.ReadFile(p.osReleasePath()) //nolint:gosec // fixed system path or test override
	if err != nil {
		return packages.OSRelease{}, fmt.Errorf("read %s: %w", p.osReleasePath(), err)
	}
	return packages.ParseOSRelease(string(data)), nil
}

// Detect reports the distribution and architecture.
func (p Platform) Detect(_ context.Context) (platform.SystemInfo, error) {
	info := platform.SystemInfo{
		OS:        "linux",
		Arch:      platform.CurrentArch(),
		Supported: platform.IsSupportedTarget("linux", platform.CurrentArch()),
	}
	if rel, err := p.osRelease(); err == nil {
		if rel.ID != "" || rel.PrettyName != "" {
			info.OSVersion = rel.Display()
		}
	}
	return info, nil
}

// DefaultInstallPath is ~/.local/opt/aseprite.
func (p Platform) DefaultInstallPath() string {
	return filepath.Join(p.InstallRoot, installDirName)
}

// exePath is where the installed executable lives.
func (p Platform) exePath() string {
	return filepath.Join(p.DefaultInstallPath(), exeName)
}

// InstalledExecutable implements platform.InstalledExecutableProvider.
func (p Platform) InstalledExecutable() (string, error) {
	return p.exePath(), nil
}

// Launch starts the installed application detached from the console
// (the CLI/TUI must not block on Aseprite's lifetime). No terminal
// emulator is assumed: the installed executable is started directly.
func (p Platform) Launch(ctx context.Context) error {
	target := p.exePath()
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("launch: %s is not installed (%w)", target, err)
	}
	cmd := runner.Command{Name: target, Dir: p.DefaultInstallPath()}
	// A detached start keeps the app alive after the CLI exits; tests
	// with FakeRunner observe the plain Run call instead.
	if d, ok := p.Runner.(runner.Detacher); ok {
		if err := d.StartDetached(ctx, cmd); err != nil {
			return fmt.Errorf("launch %s: %w", target, err)
		}
		return nil
	}
	res, err := p.Runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("launch %s: %w", target, err)
	}
	if !res.OK() {
		return fmt.Errorf("launch %s: exited with code %d", target, res.ExitCode)
	}
	return nil
}

// InstalledVersion reads the ownership marker from the installed
// directory. Returns "" when there is no installation.
func (p Platform) InstalledVersion(context.Context) (string, error) {
	m, err := readMarker(p.DefaultInstallPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return m.Version, nil
}

// Prepare currently performs no Linux-specific setup: everything the
// build needs is validated by Requirements and the per-build
// orchestration.
func (p Platform) Prepare(context.Context) error { return nil }

// marker records who installed the application. It lives next to the
// executable inside the builder-owned directory.
type marker struct {
	Version        string    `json:"aseprite_version"`
	BuilderVersion string    `json:"builder_version"`
	InstalledAt    time.Time `json:"installed_at"`
}

func markerPath(installDir string) string {
	return filepath.Join(installDir, markerName)
}

func writeMarker(installDir string, m marker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath(installDir), data, 0o644)
}

func readMarker(installDir string) (marker, error) {
	var m marker
	data, err := os.ReadFile(markerPath(installDir)) //nolint:gosec // path from trusted app logic
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse ownership marker: %w", err)
	}
	return m, nil
}

// ownsPath reports whether path is inside the builder's own install
// root — the only tree this tool may replace or remove.
func (p Platform) ownsPath(path string) bool {
	root := filepath.Clean(p.InstallRoot)
	target := filepath.Clean(path)
	return target == root || strings.HasPrefix(target, root+string(filepath.Separator))
}
