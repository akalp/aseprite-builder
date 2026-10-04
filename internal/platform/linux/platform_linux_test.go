//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/packages/apt"
	"github.com/akalp/aseprite-builder/internal/packages/dnf"
	"github.com/akalp/aseprite-builder/internal/packages/pacman"
	"github.com/akalp/aseprite-builder/internal/packages/xbps"
	"github.com/akalp/aseprite-builder/internal/packages/zypper"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

const ubuntuOSRelease = `
PRETTY_NAME="Ubuntu 24.04.1 LTS"
ID=ubuntu
ID_LIKE=debian
VERSION_ID="24.04"
VERSION_CODENAME=noble
`

const fedoraOSRelease = `
PRETTY_NAME="Fedora Linux 40 (Workstation Edition)"
ID=fedora
VERSION_ID=40
`

func writeOSRelease(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "os-release")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeManagers fabricates tool files for the listed base names and
// points every manager at either the fabricated file or a nonexistent
// path, so backend selection is deterministic regardless of the host
// distribution.
func fakeManagers(t *testing.T, r runner.Runner, present ...string) map[packages.ManagerName]packages.Manager {
	t.Helper()
	dir := t.TempDir()
	have := map[string]bool{}
	for _, name := range present {
		have[name] = true
	}
	tool := func(name string) string {
		p := filepath.Join(dir, name)
		if have[name] {
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	return map[packages.ManagerName]packages.Manager{
		packages.ManagerApt:    apt.Platform{Runner: r, APTGet: tool("apt-get"), DpkgQuery: tool("dpkg-query")},
		packages.ManagerDnf:    dnf.Platform{Runner: r, DNF: tool("dnf"), RPM: tool("rpm")},
		packages.ManagerPacman: pacman.Platform{Runner: r, Pacman: tool("pacman")},
		packages.ManagerZypper: zypper.Platform{Runner: r, Zypper: tool("zypper"), RPM: tool("rpm")},
		packages.ManagerXbps:   xbps.Platform{Runner: r, XbpsInstall: tool("xbps-install"), XbpsQuery: tool("xbps-query")},
	}
}

func newTestPlatform(t *testing.T, r runner.Runner) Platform {
	t.Helper()
	root := t.TempDir()
	return Platform{
		Runner:         r,
		InstallRoot:    filepath.Join(root, "opt"),
		CLIBinDir:      filepath.Join(root, "bin"),
		DataHome:       filepath.Join(root, "share"),
		OSReleasePath:  writeOSRelease(t, root, ubuntuOSRelease),
		BuilderVersion: "test",
		Now:            func() time.Time { return time.Unix(1700000000, 0) },
		// The Ubuntu fixture selects apt; the fabricated apt tools keep
		// selection independent of the host's real package manager.
		managerOverride: fakeManagers(t, r, "apt-get", "dpkg-query"),
	}
}

// fakeInstallPayload creates the bin directory an upstream Linux build
// produces (executable + data), without compiling anything.
func fakeInstallPayload(t *testing.T, root, version string) platform.BuildOutput {
	t.Helper()
	bin := filepath.Join(root, "build", "bin")
	if err := os.MkdirAll(filepath.Join(bin, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, exeName), []byte("ELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "data", "akey.aset"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	return platform.BuildOutput{
		Version:    version,
		BinDir:     bin,
		Executable: filepath.Join(bin, exeName),
		DataDir:    filepath.Join(bin, "data"),
	}
}

// versionFaker answers the staged/target executable's --version probe.
func versionFaker(version string) func(runner.Command) (runner.Result, bool) {
	return func(c runner.Command) (runner.Result, bool) {
		if len(c.Args) == 1 && c.Args[0] == "--version" &&
			(strings.HasSuffix(c.Name, exeName) || c.Name == exeName) {
			return runner.Result{Stdout: "Aseprite v" + version + "\n"}, true
		}
		return runner.Result{}, false
	}
}

// dpkgStatusFaker serves dpkg-query output: installed maps package
// names to "installed"; everything else is reported as absent.
func dpkgStatusFaker(installed map[string]bool) func(runner.Command) (runner.Result, bool) {
	return func(c runner.Command) (runner.Result, bool) {
		if !strings.HasSuffix(c.Name, "dpkg-query") {
			return runner.Result{}, false
		}
		var b strings.Builder
		for _, name := range c.Args {
			if strings.HasPrefix(name, "-") {
				continue
			}
			if installed[name] {
				b.WriteString(name + "\tinstalled\n")
			}
		}
		return runner.Result{Stdout: b.String(), ExitCode: 1}, true
	}
}

// rpmStatusFaker serves `rpm -q <pkg>` probes: installed packages exit
// zero, everything else exits non-zero.
func rpmStatusFaker(installed map[string]bool) func(runner.Command) (runner.Result, bool) {
	return func(c runner.Command) (runner.Result, bool) {
		if !strings.HasSuffix(c.Name, "rpm") || len(c.Args) != 2 || c.Args[0] != "-q" {
			return runner.Result{}, false
		}
		if installed[c.Args[1]] {
			return runner.Result{ExitCode: 0}, true
		}
		return runner.Result{ExitCode: 1}, true
	}
}

func findCheck(t *testing.T, checks []platform.Check, name string) platform.Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found in %+v", name, checks)
	return platform.Check{}
}

func TestDetectLinux(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	info, err := p.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.OS != "linux" || info.OSVersion != "Ubuntu 24.04.1 LTS" || info.Arch != platform.CurrentArch() {
		t.Errorf("info = %+v", info)
	}
	// This milestone targets linux/amd64; other architectures must
	// report themselves unsupported on their actual host.
	if want := platform.IsSupportedTarget("linux", platform.CurrentArch()); info.Supported != want {
		t.Errorf("Supported = %v, want %v for linux/%s", info.Supported, want, info.Arch)
	}
}

func TestDetectWithoutOSReleaseIsTolerant(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	p.OSReleasePath = filepath.Join(t.TempDir(), "missing")
	info, err := p.Detect(context.Background())
	if err != nil || info.OS != "linux" || info.OSVersion != "" {
		t.Fatalf("info = %+v err = %v", info, err)
	}
}

func TestRequirementsReportsMissingPackagesWithExactCommand(t *testing.T) {
	f := &runner.FakeRunner{Match: dpkgStatusFaker(map[string]bool{"g++": true, "cmake": true})}
	p := newTestPlatform(t, f)
	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	dist := findCheck(t, checks, "Distribution")
	if !dist.OK || !strings.Contains(dist.Detail, "Ubuntu") {
		t.Errorf("distribution check = %+v", dist)
	}
	pkg := findCheck(t, checks, "Build packages (apt)")
	if pkg.OK {
		t.Fatalf("Build packages must fail when packages are missing: %+v", pkg)
	}
	if !strings.Contains(pkg.Detail, "ninja-build") || !strings.Contains(pkg.Detail, "libx11-dev") {
		t.Errorf("Detail = %q, want the missing names", pkg.Detail)
	}
	if !strings.HasPrefix(pkg.Fix, "sudo apt-get install ") ||
		!strings.Contains(pkg.Fix, "ninja-build") || strings.Contains(pkg.Fix, "g++") {
		t.Errorf("Fix = %q, want exactly the missing packages", pkg.Fix)
	}
	findCheck(t, checks, "Disk space")
}

func TestRequirementsAllPackagesPresent(t *testing.T) {
	installed := map[string]bool{}
	for _, name := range packages.Names(apt.RequiredPackages()) {
		installed[name] = true
	}
	f := &runner.FakeRunner{Match: dpkgStatusFaker(installed)}
	p := newTestPlatform(t, f)
	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	if pkg := findCheck(t, checks, "Build packages (apt)"); !pkg.OK {
		t.Errorf("Build packages should pass: %+v", pkg)
	}
}

func TestRequirementsUnsupportedDistroFailsSafely(t *testing.T) {
	// The Fedora fixture selects the dnf family, but this platform's
	// fabricated tool set has no dnf: fail safely instead of guessing
	// another backend.
	p := newTestPlatform(t, &runner.FakeRunner{})
	p.OSReleasePath = writeOSRelease(t, t.TempDir(), fedoraOSRelease)
	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	dist := findCheck(t, checks, "Distribution")
	if dist.OK || !strings.Contains(dist.Fix, "Debian/Ubuntu") {
		t.Errorf("distribution check = %+v", dist)
	}
	for _, c := range checks {
		if strings.HasPrefix(c.Name, "Build packages") {
			t.Errorf("no package check may run on an unsupported distribution: %+v", c)
		}
	}
}

func TestRequirementsComposesWithSelectedManager(t *testing.T) {
	// A Fedora fixture with dnf tools present must use dnf's package
	// table and command, not apt's.
	f := &runner.FakeRunner{Match: rpmStatusFaker(map[string]bool{"gcc-c++": true})}
	p := newTestPlatform(t, f)
	p.OSReleasePath = writeOSRelease(t, t.TempDir(), fedoraOSRelease)
	p.managerOverride = fakeManagers(t, f, "dnf", "rpm")

	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	if dist := findCheck(t, checks, "Distribution"); !dist.OK || !strings.Contains(dist.Detail, "Fedora") {
		t.Fatalf("distribution check = %+v, want Fedora detected", dist)
	}
	pkg := findCheck(t, checks, "Build packages (dnf)")
	if pkg.OK {
		t.Fatalf("Build packages must fail when packages are missing: %+v", pkg)
	}
	if !strings.Contains(pkg.Detail, "ninja-build") || !strings.Contains(pkg.Detail, "mesa-libGL-devel") {
		t.Errorf("Detail = %q, want the dnf package names", pkg.Detail)
	}
	if !strings.HasPrefix(pkg.Fix, "sudo dnf install ") || strings.Contains(pkg.Fix, "gcc-c++") {
		t.Errorf("Fix = %q, want the dnf command with only missing packages", pkg.Fix)
	}
}

func TestRequirementsUnreadableOSReleaseFails(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	p.OSReleasePath = filepath.Join(t.TempDir(), "missing")
	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	if dist := findCheck(t, checks, "Distribution"); dist.OK {
		t.Errorf("distribution check = %+v, want failure", dist)
	}
}

func TestSystemPackagesReportsMissingAndCommand(t *testing.T) {
	f := &runner.FakeRunner{Match: dpkgStatusFaker(map[string]bool{"g++": true, "clang": true, "cmake": true})}
	p := newTestPlatform(t, f)
	report, err := p.SystemPackages(context.Background())
	if err != nil {
		t.Fatalf("SystemPackages: %v", err)
	}
	if report.Manager != "apt" {
		t.Errorf("manager = %q", report.Manager)
	}
	names := packages.Names(report.Missing)
	if len(names) == 0 || names[0] != "ninja-build" {
		t.Errorf("missing = %v, want ninja-build first", names)
	}
	if report.Command != "sudo apt-get install "+strings.Join(names, " ") {
		t.Errorf("command = %q", report.Command)
	}
}

func TestSystemPackagesUnsupportedDistroIsEmpty(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	p.OSReleasePath = writeOSRelease(t, t.TempDir(), fedoraOSRelease)
	report, err := p.SystemPackages(context.Background())
	if err != nil {
		t.Fatalf("SystemPackages: %v", err)
	}
	if report.Manager != "" || len(report.Missing) != 0 || report.Command != "" {
		t.Errorf("report = %+v, want empty", report)
	}
}

func TestSystemPackagesUsesSelectedManager(t *testing.T) {
	f := &runner.FakeRunner{Match: rpmStatusFaker(map[string]bool{"gcc-c++": true, "clang": true, "cmake": true})}
	p := newTestPlatform(t, f)
	p.OSReleasePath = writeOSRelease(t, t.TempDir(), fedoraOSRelease)
	p.managerOverride = fakeManagers(t, f, "dnf", "rpm")

	report, err := p.SystemPackages(context.Background())
	if err != nil {
		t.Fatalf("SystemPackages: %v", err)
	}
	if report.Manager != "dnf" {
		t.Fatalf("manager = %q, want dnf", report.Manager)
	}
	if !strings.HasPrefix(report.Command, "sudo dnf install ") || !strings.Contains(report.Command, "ninja-build") {
		t.Errorf("command = %q, want a dnf command for the missing packages", report.Command)
	}
	if strings.Contains(report.Command, "gcc-c++") {
		t.Errorf("command = %q must list only missing packages", report.Command)
	}
}

func TestInstallSystemPackagesRunsInteractiveSudo(t *testing.T) {
	f := &runner.FakeRunner{}
	p := newTestPlatform(t, f)
	if err := p.InstallSystemPackages(context.Background(), []packages.Package{{Name: "cmake"}}); err != nil {
		t.Fatalf("InstallSystemPackages: %v", err)
	}
	calls := f.Called()
	if len(calls) != 1 || calls[0].Name != "sudo" || !calls[0].Interactive {
		t.Fatalf("calls = %+v, want one interactive sudo", calls)
	}
	if got := strings.Join(calls[0].Args, " "); got != "apt-get install cmake" {
		t.Errorf("args = %q", got)
	}
}

func TestInstallLifecycleRetentionAndIntegration(t *testing.T) {
	f := &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	p := newTestPlatform(t, f)

	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	out.IconsDir = fixtureIcons(t)
	out.DesktopFile = fixtureDesktopFile(t)

	res, err := p.Install(context.Background(), out)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.AppPath != p.DefaultInstallPath() || res.PreviousPath != "" {
		t.Errorf("outcome = %+v", res)
	}
	m, err := readMarker(p.DefaultInstallPath())
	if err != nil || m.Version != "1.3.18.6" || m.BuilderVersion != "test" {
		t.Fatalf("marker = %+v err = %v", m, err)
	}

	// CLI symlink points at the installed executable.
	link := filepath.Join(p.CLIBinDir, exeName)
	target, err := os.Readlink(link)
	if err != nil || target != p.exePath() {
		t.Errorf("symlink = %q err = %v, want %q", target, err, p.exePath())
	}

	// Desktop entry: upstream template with Exec/TryExec rewritten and
	// the ownership marker prepended.
	entry, err := os.ReadFile(p.desktopEntryPath())
	if err != nil {
		t.Fatalf("desktop entry: %v", err)
	}
	text := string(entry)
	if !strings.HasPrefix(text, desktopMarker) {
		t.Errorf("desktop entry missing marker:\n%s", text)
	}
	if !strings.Contains(text, "Exec="+p.exePath()+" %U") ||
		!strings.Contains(text, "TryExec="+p.exePath()) {
		t.Errorf("desktop entry not pointed at the install:\n%s", text)
	}
	if !strings.Contains(text, "MimeType=image/bmp;image/x-aseprite;") {
		t.Errorf("upstream metadata lost:\n%s", text)
	}

	// Icons landed under hicolor sizes read from the PNG headers.
	for _, size := range []string{"16x16", "256x256", "512x512"} {
		icon := filepath.Join(p.iconThemeDir(), size, "apps", exeName+".png")
		if _, err := os.Stat(icon); err != nil {
			t.Errorf("icon %s missing: %v", icon, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.iconThemeDir(), "16x16", "apps", "doc16.png")); !os.IsNotExist(err) {
		t.Error("document icons must not be installed as the app icon")
	}
	if _, err := os.Stat(filepath.Join(p.iconThemeDir(), "1024x1024")); !os.IsNotExist(err) {
		t.Error("alternate artwork (asehd-blush.png) must not be installed as the app icon")
	}

	// Update: the previous version is retained, integration refreshed.
	p.Runner = &runner.FakeRunner{Match: versionFaker("1.3.18.7")}
	out2 := fakeInstallPayload(t, t.TempDir(), "1.3.18.7")
	out2.IconsDir = fixtureIcons(t)
	out2.DesktopFile = fixtureDesktopFile(t)
	res2, err := p.Install(context.Background(), out2)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if res2.PreviousPath == "" {
		t.Fatal("previous installation must be retained")
	}
	prevMarker, err := readMarker(p.DefaultInstallPath()) // current is now 1.3.18.7
	if err != nil || prevMarker.Version != "1.3.18.7" {
		t.Fatalf("current marker = %+v err = %v", prevMarker, err)
	}
	old, err := readMarker(res2.PreviousPath)
	if err != nil || old.Version != "1.3.18.6" {
		t.Fatalf("previous marker = %+v err = %v", old, err)
	}
}

func TestFailedUpdateKeepsPreviousInstallation(t *testing.T) {
	f := &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	p := newTestPlatform(t, f)
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	// The new build's executable reports the wrong version: activation
	// must never happen and the old installation must stay intact.
	out2 := fakeInstallPayload(t, t.TempDir(), "1.3.18.7")
	if _, err := p.Install(context.Background(), out2); err == nil {
		t.Fatal("a version-mismatched build must fail verification")
	}
	m, err := readMarker(p.DefaultInstallPath())
	if err != nil || m.Version != "1.3.18.6" {
		t.Fatalf("target marker = %+v err = %v, want the untouched old version", m, err)
	}
	if _, err := os.Stat(p.exePath()); err != nil {
		t.Errorf("installed executable missing after failed update: %v", err)
	}
}

func TestInstallRefusesUnownedTargetUnlessForced(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{Match: versionFaker("1.3.18.6")})
	target := p.DefaultInstallPath()
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	if _, err := p.Install(context.Background(), out); err == nil ||
		!strings.Contains(err.Error(), "not managed by aseprite-builder") {
		t.Fatalf("err = %v, want the unowned-target refusal", err)
	}

	p.AllowReplaceUnowned = true
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("forced Install: %v", err)
	}
}

func TestForeignCLIBinariesAreNeverClobbered(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{Match: versionFaker("1.3.18.6")})
	if err := os.MkdirAll(p.CLIBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "some-other-aseprite")
	link := filepath.Join(p.CLIBinDir, exeName)
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("Install must not fail on a foreign symlink: %v", err)
	}
	if got, _ := os.Readlink(link); got != foreign {
		t.Errorf("foreign symlink was modified: %q", got)
	}

	// Uninstall must leave the foreign link alone too.
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if got, _ := os.Readlink(link); got != foreign {
		t.Errorf("uninstall removed/modified a foreign symlink: %q", got)
	}
}

func TestForeignRegularFileAtCLIPathIsLeftUntouched(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{Match: versionFaker("1.3.18.6")})
	if err := os.MkdirAll(p.CLIBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.CLIBinDir, exeName)
	if err := os.WriteFile(link, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("Install must not fail on a foreign file: %v", err)
	}
	if data, err := os.ReadFile(link); err != nil || string(data) != "#!/bin/sh\n" {
		t.Errorf("foreign file modified: %q err=%v", data, err)
	}
}

func TestForeignDesktopEntryIsNeverClobberedOrRemoved(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{Match: versionFaker("1.3.18.6")})
	if err := os.MkdirAll(p.applicationsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "[Desktop Entry]\nType=Application\nName=Aseprite\nExec=/usr/bin/aseprite\n"
	if err := os.WriteFile(p.desktopEntryPath(), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("Install must not fail on a foreign desktop entry: %v", err)
	}
	if data, _ := os.ReadFile(p.desktopEntryPath()); string(data) != foreign {
		t.Errorf("foreign desktop entry was modified:\n%s", data)
	}
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(p.desktopEntryPath()); err != nil {
		t.Errorf("uninstall removed a foreign desktop entry: %v", err)
	}
}

func TestUninstallRequiresOwnership(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	if err := os.MkdirAll(p.DefaultInstallPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	err := p.Uninstall(context.Background(), platform.UninstallOptions{})
	if !errors.Is(err, platform.ErrNotOwned) {
		t.Fatalf("err = %v, want ErrNotOwned", err)
	}
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{Force: true}); err != nil {
		t.Fatalf("forced Uninstall: %v", err)
	}
	if _, err := os.Stat(p.DefaultInstallPath()); !os.IsNotExist(err) {
		t.Error("forced uninstall must remove the target")
	}
}

func TestUninstallRemovesOwnedIntegrationOnly(t *testing.T) {
	f := &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	p := newTestPlatform(t, f)
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	out.IconsDir = fixtureIcons(t)
	out.DesktopFile = fixtureDesktopFile(t)
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{RemovePrevious: true}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, gone := range []string{
		p.DefaultInstallPath(),
		filepath.Join(p.CLIBinDir, exeName),
		p.desktopEntryPath(),
		filepath.Join(p.iconThemeDir(), "16x16", "apps", exeName+".png"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still exists after uninstall", gone)
		}
	}
}

func TestLaunchPrefersDetachedStart(t *testing.T) {
	base := &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	p := newTestPlatform(t, base)
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("Install: %v", err)
	}

	d := &detachRunner{FakeRunner: base}
	p.Runner = d
	if err := p.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if len(d.detached) != 1 || d.detached[0].Name != p.exePath() {
		t.Errorf("detached = %+v, want the installed executable", d.detached)
	}
}

func TestLaunchWithoutDetacherFallsBackToRun(t *testing.T) {
	f := &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	p := newTestPlatform(t, f)
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("Install: %v", err)
	}
	f.Reset()
	if err := p.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if calls := f.CalledStrings(); len(calls) != 1 || calls[0] != p.exePath() {
		t.Errorf("calls = %v", calls)
	}
}

func TestPngSizeReadsHeader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "icon.png")
	if err := os.WriteFile(p, pngFixture(48, 48), 0o644); err != nil {
		t.Fatal(err)
	}
	w, h, err := pngSize(p)
	if err != nil || w != 48 || h != 48 {
		t.Fatalf("pngSize = %d,%d err=%v", w, h, err)
	}

	bad := filepath.Join(dir, "bad.png")
	if err := os.WriteFile(bad, []byte("not a png at all, but 24 bytes!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pngSize(bad); err == nil {
		t.Error("a non-PNG must fail the header parse")
	}
}

// detachRunner records StartDetached calls; embedding FakeRunner keeps
// the plain Run capability for non-detached invocations.
type detachRunner struct {
	*runner.FakeRunner
	detached []runner.Command
}

func (d *detachRunner) StartDetached(_ context.Context, cmd runner.Command) error {
	d.detached = append(d.detached, cmd)
	return nil
}

// pngFixture builds the smallest valid-enough PNG header (signature +
// IHDR length/type + dimensions).
func pngFixture(width, height uint32) []byte {
	b := make([]byte, 24)
	b[0], b[1], b[2], b[3] = 0x89, 'P', 'N', 'G'
	b[4], b[5], b[6], b[7] = 0x0d, 0x0a, 0x1a, 0x0a
	binary.BigEndian.PutUint32(b[8:12], 13) // IHDR length
	copy(b[12:16], "IHDR")
	binary.BigEndian.PutUint32(b[16:20], width)
	binary.BigEndian.PutUint32(b[20:24], height)
	return b
}

// fixtureIcons writes a small upstream-shaped icon set: size-named
// application icons plus document icons (which must be filtered out)
// and the hd/ large icon (including alternate artwork that must not be
// mistaken for it).
func fixtureIcons(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{
		"ase16.png":          pngFixture(16, 16),
		"ase256.png":         pngFixture(256, 256),
		"doc16.png":          pngFixture(16, 16),
		"ext16.png":          pngFixture(16, 16),
		"hd/asehd.png":       pngFixture(512, 512),
		"hd/asehd-blush.png": pngFixture(1024, 1024),
	}
	for name, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fixtureDesktopFile writes the upstream Linux desktop entry shape.
func fixtureDesktopFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "aseprite.desktop")
	content := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Aseprite\n" +
		"GenericName=Sprite Editor\n" +
		"Comment=Animated sprite editor & pixel art tool\n" +
		"Icon=aseprite\n" +
		"Categories=Graphics;2DGraphics;RasterGraphics\n" +
		"Exec=aseprite %U\n" +
		"TryExec=aseprite\n" +
		"Terminal=false\n" +
		"StartupNotify=false\n" +
		"StartupWMClass=Aseprite\n" +
		"MimeType=image/bmp;image/x-aseprite;\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
