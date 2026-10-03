//go:build darwin

// Package darwin implements platform.Platform for macOS. It manages
// only what macOS requires of aseprite-builder: detecting the
// system-managed Apple toolchain, installing a native Aseprite.app
// bundle in the user's own Applications folder, launching, updating
// transactionally, and uninstalling builder-owned bundles.
//
// Xcode, the macOS SDK and the compiler are system-managed: this
// package detects them and reports remediation hints, but never
// installs or bundles them.
package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// minFreeDiskBytes guards the install flow: Skia (~1 GiB), the source
// archive (~800 MiB), extracted trees (~3 GiB) and the build (~2 GiB)
// all share the user's disk.
const minFreeDiskBytes = 10 << 30

// markerName is the ownership marker installed inside every
// builder-created bundle. Uninstall refuses to touch bundles without it.
//
// It lives in Contents/Resources/ (data, not code): a dotfile at
// Contents/ root would be treated as unsigned nested code by codesign
// and break the bundle's ad-hoc signature entirely.
const markerName = ".aseprite-builder-managed"

// appBundleName is the name of the macOS application bundle Aseprite's
// upstream build produces (see src/CMakeLists.txt: bin/Aseprite.app).
const appBundleName = "Aseprite.app"

// retentionDir holds the previous installation between updates. Only
// builder-owned bundles are stored or removed here.
const retentionDirName = ".aseprite-builder"

// Platform implements platform.Platform for macOS.
type Platform struct {
	// Runner executes external commands (injected for tests).
	Runner runner.Runner
	// InstallRoot is the user's Applications directory (~/Applications).
	InstallRoot string
	// BuilderVersion recorded in the ownership marker.
	BuilderVersion string
	// AllowReplaceUnowned lets install overwrite an existing Aseprite.app
	// the builder cannot prove it owns. The user must opt in explicitly.
	AllowReplaceUnowned bool
	// Logger receives non-fatal operational notes (nil disables).
	Logger warningLogger
	// Now overrides wall-clock time in tests.
	Now func() time.Time
}

// logger returns a never-nil warning logger.
func (p Platform) logger() warningLogger {
	if p.Logger != nil {
		return p.Logger
	}
	return nopLogger{}
}

// nopLogger discards warnings (used when no logger is wired).
type nopLogger struct{}

func (nopLogger) Line(string, ...any) {}

// New returns a macOS Platform with the default install root.
func New(r runner.Runner, builderVersion string) (Platform, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Platform{}, fmt.Errorf("resolve home dir: %w", err)
	}
	return Platform{
		Runner:         r,
		InstallRoot:    filepath.Join(home, "Applications"),
		BuilderVersion: builderVersion,
		Now:            time.Now,
	}, nil
}

// SetAllowReplaceUnowned implements the optional consent interface the
// app service uses when the user explicitly authorizes replacing an
// existing bundle the builder cannot prove it owns.
func (p *Platform) SetAllowReplaceUnowned(v bool) { p.AllowReplaceUnowned = v }

// Detect reports the macOS version and architecture.
func (p Platform) Detect(_ context.Context) (platform.SystemInfo, error) {
	info := platform.SystemInfo{
		OS:        "darwin",
		Arch:      platform.CurrentArch(),
		Supported: platform.IsSupportedTarget("darwin", platform.CurrentArch()),
	}
	res, err := p.Runner.Run(context.Background(), runner.Command{
		Name: "sw_vers",
		Args: []string{"-productVersion"},
	})
	if err == nil {
		info.OSVersion = strings.TrimSpace(res.Stdout)
	}
	return info, nil
}

// Requirements checks the system-managed Apple toolchain: command line
// tools (compiler) and macOS SDK, plus free disk space.
func (p Platform) Requirements(ctx context.Context) ([]platform.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var checks []platform.Check

	// 1. Xcode command line tools / Xcode
	clt, cltErr := p.Runner.Run(ctx, runner.Command{Name: "xcode-select", Args: []string{"-p"}})
	if cltErr == nil && clt.OK() {
		path := strings.TrimSpace(clt.Stdout)
		checks = append(checks, platform.Check{
			Name:   "Apple toolchain (Xcode / Command Line Tools)",
			OK:     true,
			Detail: path,
			Source: "system",
		})
	} else {
		checks = append(checks, platform.Check{
			Name: "Apple toolchain (Xcode / Command Line Tools)",
			OK:   false,
			Fix:  "Run: xcode-select --install (then reopen your terminal)",
		})
	}

	// 2. C/C++ compiler
	if clang, err := p.Runner.Run(ctx, runner.Command{Name: "xcrun", Args: []string{"--find", "clang"}}); err == nil && clang.OK() {
		checks = append(checks, platform.Check{
			Name:   "Compiler (Apple Clang)",
			OK:     true,
			Detail: strings.TrimSpace(clang.Stdout),
			Source: "system",
		})
	} else {
		checks = append(checks, platform.Check{
			Name: "Compiler (Apple Clang)",
			OK:   false,
			Fix:  "Install Xcode Command Line Tools with: xcode-select --install",
		})
	}

	// 3. macOS SDK
	if sdk, err := p.Runner.Run(ctx, runner.Command{Name: "xcrun", Args: []string{"--show-sdk-path"}}); err == nil && sdk.OK() {
		checks = append(checks, platform.Check{
			Name:   "macOS SDK",
			OK:     true,
			Detail: strings.TrimSpace(sdk.Stdout),
			Source: "system",
		})
	} else {
		checks = append(checks, platform.Check{
			Name: "macOS SDK",
			OK:   false,
			Fix:  "Install Xcode Command Line Tools with: xcode-select --install",
		})
	}

	// 4. Disk space (non-fatal warning level)
	free, err := freeDiskBytes(p.InstallRoot)
	switch {
	case err != nil:
		checks = append(checks, platform.Check{Name: "Disk space", OK: false, Fix: "Check free space on your startup disk"})
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

// Prepare currently performs no macOS-specific setup: everything the
// build needs is validated by Requirements and per-build orchestration.
func (p Platform) Prepare(context.Context) error { return nil }

// DefaultInstallPath is ~/Applications/Aseprite.app.
func (p Platform) DefaultInstallPath() string {
	return filepath.Join(p.InstallRoot, appBundleName)
}

type marker struct {
	Version        string    `json:"aseprite_version"`
	BuilderVersion string    `json:"builder_version"`
	InstalledAt    time.Time `json:"installed_at"`
}

// Install activates a verified build output transactionally:
//
//  1. copy the bundle into a staging dir under InstallRoot
//  2. ad-hoc sign and verify it (run --version) BEFORE touching target
//  3. move any existing (builder-owned) installation into retention
//  4. activate staged bundle
//
// On any failure the previous installation stays exactly as it was.
func (p Platform) Install(ctx context.Context, out platform.BuildOutput) (platform.InstallOutcome, error) {
	if out.AppPath == "" {
		return platform.InstallOutcome{}, errors.New("build output has no application bundle")
	}
	if fi, err := os.Stat(out.AppPath); err != nil || !fi.IsDir() {
		return platform.InstallOutcome{}, fmt.Errorf("built bundle %s is missing: %w", out.AppPath, err)
	}

	target := p.DefaultInstallPath()
	if err := os.MkdirAll(p.InstallRoot, 0o755); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("create install dir: %w", err)
	}
	if _, err := os.Stat(target); err == nil && !p.allowOverwrite(ctx, target) {
		return platform.InstallOutcome{}, fmt.Errorf("%s exists and is not managed by aseprite-builder; "+
			"pass --force to replace it explicitly", target)
	}

	// Staging under the install root: same filesystem, so activation is a
	// rename (atomic swap candidate).
	stagingRoot := filepath.Join(p.InstallRoot, retentionDirName, "staging")
	if err := os.RemoveAll(stagingRoot); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("clear staging dir: %w", err)
	}
	staged := filepath.Join(stagingRoot, appBundleName)
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("create staging dir: %w", err)
	}
	if err := copyDir(out.AppPath, staged); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("stage bundle: %w", err)
	}

	now := time.Now
	if p.Now != nil {
		now = p.Now
	}

	// Generate .icns bundle icons from the upstream icon set. This adds
	// content to the staged bundle and must happen BEFORE the ownership
	// marker and ad-hoc signing, which seal its contents.
	if out.IconsDir != "" {
		if err := generateBundleIcons(ctx, p.Runner, staged, out.IconsDir, p.logger()); err != nil {
			// Best-effort: icon generation never blocks installation.
			if p.Logger != nil {
				p.Logger.Line("note: icon generation skipped: %v", err)
			}
		}
	}

	if err := writeMarker(staged, marker{
		Version:        out.Version,
		BuilderVersion: p.BuilderVersion,
		InstalledAt:    now().UTC(),
	}); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("write ownership marker: %w", err)
	}

	// Ad-hoc signing: sign AFTER all content is in place. Failure to find
	// codesign is tolerated (toolchain already ad-hoc signs binaries);
	// a codesign error aborts before activation.
	if err := p.codesign(ctx, staged); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("sign staged application: %w", err)
	}

	// Verify the staged bundle actually runs before replacing anything.
	if err := p.verifyStaged(ctx, staged, out.Version); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("verify staged application: %w", err)
	}

	// Retain the previous builder-owned installation for rollback.
	previous := ""
	retention := filepath.Join(p.InstallRoot, retentionDirName, "previous")
	if _, err := os.Stat(target); err == nil {
		previous = filepath.Join(retention, appBundleName)
		if err := os.RemoveAll(previous); err != nil {
			return platform.InstallOutcome{}, fmt.Errorf("clear previous retention: %w", err)
		}
		if err := os.MkdirAll(retention, 0o755); err != nil {
			return platform.InstallOutcome{}, fmt.Errorf("create retention dir: %w", err)
		}
		if err := os.Rename(target, previous); err != nil {
			return platform.InstallOutcome{}, fmt.Errorf("retain previous installation: %w", err)
		}
	}
	activated := false
	defer func() {
		if !activated {
			// Rollback: put the previous installation back in place.
			if previous != "" {
				_ = os.Remove(target)
				_ = os.Rename(previous, target)
			}
		}
	}()

	if err := os.Rename(staged, target); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("activate installation: %w", err)
	}
	activated = true

	if err := os.RemoveAll(stagingRoot); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("clean staging dir: %w", err)
	}

	// Register with LaunchServices so Spotlight, Raycast, Dock and file
	// associations see the new bundle immediately. Failures are
	// non-fatal: macOS also registers lazily on first launch.
	p.registerWithLaunchServices(ctx, target)

	return platform.InstallOutcome{
		AppPath:        target,
		PreviousPath:   previous,
		StagingRemoved: true,
	}, nil
}

// lsregisterPath is the system LaunchService registration tool. It is
// part of macOS itself (no Xcode needed) and runs unprivileged with -f.
const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

// registerWithLaunchServices forces the LaunchServices database to
// refresh the bundle. Best-effort.
func (p Platform) registerWithLaunchServices(ctx context.Context, app string) {
	res, err := p.Runner.Run(ctx, runner.Command{Name: lsregisterPath, Args: []string{"-f", app}})
	if err != nil {
		return // tool absent or intercepted; lazy registration still works
	}
	if !res.OK() && p.Logger != nil {
		p.Logger.Line("note: LaunchServices registration exited %d for %s", res.ExitCode, app)
	}
}

// allowOverwrite reports whether an existing target may be replaced.
func (p Platform) allowOverwrite(ctx context.Context, target string) bool {
	if _, err := readMarker(target); err == nil {
		return true // builder-owned: safe to replace transactionally
	}
	return p.AllowReplaceUnowned
}

// Launch opens the installed application with macOS's open(1).
func (p Platform) Launch(ctx context.Context) error {
	target := p.DefaultInstallPath()
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("launch: %s is not installed (%w)", target, err)
	}
	res, err := p.Runner.Run(ctx, runner.Command{Name: "open", Args: []string{target}})
	if err != nil {
		return fmt.Errorf("launch %s: %w", target, err)
	}
	if !res.OK() {
		return fmt.Errorf("launch %s: open exited with code %d", target, res.ExitCode)
	}
	return nil
}

// Uninstall removes the builder-owned application and, when asked, the
// retained previous installation. Ownership is proven via the marker
// file; without it the operation fails rather than guessing.
func (p Platform) Uninstall(_ context.Context, opts platform.UninstallOptions) error {
	target := p.DefaultInstallPath()
	_, err := readMarker(target)
	switch {
	case err == nil:
		// owned, continue
	case opts.Force:
		// explicit override; still log-worthy but allowed
	default:
		return fmt.Errorf("%w: %s", platform.ErrNotOwned, target)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove %s: %w", target, err)
	}
	if opts.RemovePrevious {
		prev := filepath.Join(p.InstallRoot, retentionDirName, "previous", appBundleName)
		if err := os.RemoveAll(prev); err != nil {
			return fmt.Errorf("remove previous installation: %w", err)
		}
	}
	return nil
}

// InstalledVersion reads the ownership marker from the installed bundle.
// Returns "" when there is no installation.
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

func (p Platform) codesign(ctx context.Context, app string) error {
	res, err := p.Runner.Run(ctx, runner.Command{
		Name: "codesign",
		Args: []string{"--force", "--sign", "-", app},
	})
	if err != nil {
		// codesign missing from PATH is tolerated (the linker already
		// ad-hoc signs the executable); a codesign FAILURE is a real
		// problem and must abort the install.
		var exitErr runner.ErrExit
		if errors.As(err, &exitErr) {
			return fmt.Errorf("codesign failed (exit %d): %s", exitErr.ExitCode, exitErr.Stderr)
		}
		return nil //nolint:nilerr // absence of codesign is tolerated by design
	}
	if !res.OK() {
		return fmt.Errorf("codesign exited with code %d", res.ExitCode)
	}
	return nil
}

// verifyStaged runs the staged executable with --version, requiring a
// clean exit and a version match before anything is activated.
//
// HOME is redirected to a throwaway directory so any first-run Aseprite
// writes land in the sandbox instead of the user's real
// Application Support during verification.
func (p Platform) verifyStaged(ctx context.Context, app, wantVersion string) error {
	executable, err := bundleExecutable(app)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	sandbox, err := os.MkdirTemp("", "aseprite-builder-verify-")
	if err != nil {
		return fmt.Errorf("create verification sandbox: %w", err)
	}
	defer func() { _ = os.RemoveAll(sandbox) }()

	res, err := p.Runner.Run(ctx, runner.Command{
		Name: executable,
		Args: []string{"--version"},
		Env:  []string{"HOME=" + sandbox},
	})
	if err != nil {
		return fmt.Errorf("run --version: %w", err)
	}
	if !res.OK() {
		return fmt.Errorf("--version exited with code %d", res.ExitCode)
	}
	stdout := strings.TrimSpace(res.Stdout)
	if !strings.Contains(stdout, wantVersion) {
		return fmt.Errorf("version check: got %q, want a string containing %q", stdout, wantVersion)
	}
	return nil
}

// bundleExecutable resolves the Mach-O executable inside a bundle.
func bundleExecutable(app string) (string, error) {
	p := filepath.Join(app, "Contents", "MacOS", "aseprite")
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("bundle %s has no executable: %w", app, err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("bundle %s executable is not a file", app)
	}
	return p, nil
}

// markerPath is where the ownership marker lives inside a bundle:
// Contents/Resources (sealed as a resource, not treated as code).
func markerPath(app string) string {
	return filepath.Join(app, "Contents", "Resources", markerName)
}

func writeMarker(app string, m marker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath(app), data, 0o644)
}

// readMarker reads the ownership marker, accepting both the current
// location (Contents/Resources) and the legacy initial location
// (Contents/) so installs made before the signing fix can still be
// proven owned and replaced transactionally.
func readMarker(app string) (marker, error) {
	var m marker
	data, err := os.ReadFile(markerPath(app)) //nolint:gosec // path from trusted app logic
	if err != nil {
		data, err = os.ReadFile(filepath.Join(app, "Contents", markerName)) //nolint:gosec // legacy path, trusted app logic
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse ownership marker: %w", err)
	}
	return m, nil
}

// copyDir recursively copies a directory tree preserving modes.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			// Symlinks inside the bundle (e.g. bin/aseprite -> Aseprite.app
			// helper) are recreated relative when possible; anything exotic
			// fails the install rather than being guessed at.
			if info.Mode()&os.ModeSymlink != 0 {
				return copySymlink(path, target)
			}
			return fmt.Errorf("unsupported file type %v in bundle: %s", info.Mode(), path)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copySymlink(src, dst string) error {
	linkTarget, err := os.Readlink(src)
	if err != nil {
		return err
	}
	_ = os.Remove(dst)
	return os.Symlink(linkTarget, dst)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // path from trusted app logic
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(in, maxCopyBytes)); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

const maxCopyBytes = 2 << 30

// ioCopyN copies with a size cap so a pathological bundle never fills
// the disk.
func ioCopyN(out, in *os.File) (int64, error) {
	return io.Copy(out, io.LimitReader(in, maxCopyBytes)) //nolint:gosec // writing to a path built from trusted logic
}
