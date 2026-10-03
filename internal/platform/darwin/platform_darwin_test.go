//go:build darwin

package darwin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aseprite-builder/aseprite-builder/internal/platform"
	"github.com/aseprite-builder/aseprite-builder/internal/runner"
)

// fakeApp creates a minimal stand-in for the Aseprite.app bundle
// produced by an upstream build. No real Aseprite compilation occurs.
func fakeApp(t *testing.T, root, version string) string {
	t.Helper()
	app := filepath.Join(root, appBundleName)
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app, "Contents", "Resources", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Resources", "data", "dummy.dat"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "aseprite"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = version
	return app
}

// versionFaker returns a runner matcher that answers any
// `--version` invocation with the given Aseprite version string,
// mimicking a freshly built executable without running one.
func versionFaker(version string) func(runner.Command) (runner.Result, bool) {
	return func(c runner.Command) (runner.Result, bool) {
		if len(c.Args) == 1 && c.Args[0] == "--version" && strings.HasSuffix(c.Name, "aseprite") {
			return runner.Result{Stdout: "Aseprite v" + version + "\n"}, true
		}
		return runner.Result{}, false
	}
}

func newTestPlatform(t *testing.T, r runner.Runner) Platform {
	t.Helper()
	return Platform{
		Runner:         r,
		InstallRoot:    filepath.Join(t.TempDir(), "Applications"),
		BuilderVersion: "test",
		Now:            func() time.Time { return time.Unix(1700000000, 0) },
	}
}

// fakeVersionRunner wires versionFaker into a FakeRunner for install tests.
func fakeVersionRunner(version string) *runner.FakeRunner {
	return &runner.FakeRunner{Match: versionFaker(version)}
}

func TestDetectDarwin(t *testing.T) {
	f := &runner.FakeRunner{Results: map[string]runner.Result{
		"sw_vers -productVersion": {Stdout: "15.2\n"},
	}}
	p := newTestPlatform(t, f)
	info, err := p.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.OS != "darwin" || info.OSVersion != "15.2" || info.Arch != platform.CurrentArch() {
		t.Errorf("info = %+v", info)
	}
	if !info.Supported {
		t.Error("running host should be a supported target in tests")
	}
}

func TestRequirementsAllPresent(t *testing.T) {
	f := &runner.FakeRunner{Results: map[string]runner.Result{
		"xcode-select -p":       {Stdout: "/Library/Developer/CommandLineTools\n"},
		"xcrun --find clang":    {Stdout: "/usr/bin/clang\n"},
		"xcrun --show-sdk-path": {Stdout: "/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk\n"},
	}}
	p := newTestPlatform(t, f)
	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	byName := map[string]platform.Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	for _, name := range []string{
		"Apple toolchain (Xcode / Command Line Tools)",
		"Compiler (Apple Clang)",
		"macOS SDK",
	} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("missing check %q in %+v", name, byName)
		}
		if !c.OK {
			t.Errorf("check %q should be OK: %+v", name, c)
		}
	}
	// Disk space may be OK or fail depending on the temp dir; never panic.
	if _, ok := byName["Disk space"]; !ok {
		t.Error("Disk space check missing")
	}
}

func TestRequirementsMissingToolchain(t *testing.T) {
	f := &runner.FakeRunner{Errs: map[string]error{
		"xcode-select -p": runner.ErrExit{ExitCode: 2},
	}}
	p := newTestPlatform(t, f)
	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	var clt *platform.Check
	for i := range checks {
		if checks[i].Name == "Apple toolchain (Xcode / Command Line Tools)" {
			clt = &checks[i]
		}
	}
	if clt == nil || clt.OK {
		t.Fatal("missing CLT should be reported as not OK")
	}
	if !strings.Contains(clt.Fix, "xcode-select --install") {
		t.Errorf("fix hint = %q", clt.Fix)
	}
}

func TestInstallFresh(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, fakeVersionRunner("1.3.18.6"))
	built := fakeApp(t, dir, "1.3.18.6")

	// The staged verification command targets the staged path; fake it.
	p.InstallRoot = filepath.Join(dir, "Applications")

	out := platform.BuildOutput{Version: "1.3.18.6", AppPath: built}
	outcome, err := p.Install(context.Background(), out)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	target := p.DefaultInstallPath()
	if outcome.AppPath != target {
		t.Errorf("AppPath = %q, want %q", outcome.AppPath, target)
	}
	if _, err := os.Stat(filepath.Join(target, "Contents", "MacOS", "aseprite")); err != nil {
		t.Errorf("installed executable missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "Contents", markerName)); err != nil {
		t.Errorf("ownership marker missing: %v", err)
	}
	// Staging must be cleaned up.
	staging := filepath.Join(p.InstallRoot, retentionDirName, "staging")
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Error("staging dir should be removed after successful install")
	}
	// No previous existed before.
	if outcome.PreviousPath != "" {
		t.Errorf("PreviousPath = %q, want empty on fresh install", outcome.PreviousPath)
	}
}

func TestInstallReplacesAndRetainsPrevious(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "Applications")
	p := newTestPlatform(t, fakeVersionRunner("1.3.18.5"))
	p.InstallRoot = root

	// First install
	f := fakeVersionRunner("1.3.18.5")
	p = newTestPlatform(t, f)
	p.InstallRoot = root
	first := fakeApp(t, filepath.Join(dir, "build1"), "1.3.18.5")
	if _, err := p.Install(context.Background(), platform.BuildOutput{Version: "1.3.18.5", AppPath: first}); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	// Second install replaces the first
	f.Match = versionFaker("1.3.18.6")
	second := fakeApp(t, filepath.Join(dir, "build2"), "1.3.18.6")
	outcome, err := p.Install(context.Background(), platform.BuildOutput{Version: "1.3.18.6", AppPath: second})
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if outcome.PreviousPath == "" {
		t.Fatal("previous installation should be retained")
	}
	m, err := readMarker(outcome.PreviousPath)
	if err != nil {
		t.Fatalf("read retained previous: %v", err)
	}
	if m.Version != "1.3.18.5" {
		t.Errorf("retained previous version = %q, want 1.3.18.5", m.Version)
	}
	// Target is now the new version
	got, err := p.InstalledVersion(context.Background())
	if err != nil || got != "1.3.18.6" {
		t.Errorf("InstalledVersion = %q, %v; want 1.3.18.6", got, err)
	}
}

func TestInstallFailureKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, fakeVersionRunner("1.3.18.5"))
	p.InstallRoot = filepath.Join(dir, "Applications")

	first := fakeApp(t, filepath.Join(dir, "build1"), "1.3.18.5")
	if _, err := p.Install(context.Background(), platform.BuildOutput{Version: "1.3.18.5", AppPath: first}); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	// Break the second build artifact so verification fails: make the
	// bundle executable unreadable.
	broken := fakeApp(t, filepath.Join(dir, "build2"), "1.3.18.6")
	if err := os.Remove(filepath.Join(broken, "Contents", "MacOS", "aseprite")); err != nil {
		t.Fatal(err)
	}
	_, err := p.Install(context.Background(), platform.BuildOutput{Version: "1.3.18.6", AppPath: broken})
	if err == nil {
		t.Fatal("install of broken bundle should fail")
	}
	// Existing installation untouched.
	got, err := p.InstalledVersion(context.Background())
	if err != nil || got != "1.3.18.5" {
		t.Errorf("InstalledVersion after failed update = %q, %v; want 1.3.18.5 intact", got, err)
	}
}

func TestInstallRefusesUnownedTarget(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, &runner.FakeRunner{})
	p.InstallRoot = filepath.Join(dir, "Applications")

	// An Aseprite.app that the builder does not own.
	target := p.DefaultInstallPath()
	if err := os.MkdirAll(filepath.Join(target, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}

	built := fakeApp(t, filepath.Join(dir, "build1"), "1.3.18.6")
	if _, err := p.Install(context.Background(), platform.BuildOutput{Version: "1.3.18.6", AppPath: built}); err == nil {
		t.Fatal("install must refuse to replace an unowned bundle without --force")
	}
	if got, _ := p.InstalledVersion(context.Background()); got != "" {
		t.Error("unowned target must not gain a builder marker")
	}

	// With AllowReplaceUnowned the install proceeds and the unowned bundle
	// is moved to retention.
	p.AllowReplaceUnowned = true
	p.Runner = fakeVersionRunner("1.3.18.6")
	if _, err := p.Install(context.Background(), platform.BuildOutput{Version: "1.3.18.6", AppPath: built}); err != nil {
		t.Fatalf("forced install: %v", err)
	}
	got, _ := p.InstalledVersion(context.Background())
	if got != "1.3.18.6" {
		t.Errorf("InstalledVersion = %q, want 1.3.18.6", got)
	}
}

func TestUninstallOwnedAndNotOwned(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, fakeVersionRunner("1.3.18.6"))
	p.InstallRoot = filepath.Join(dir, "Applications")

	app := fakeApp(t, filepath.Join(dir, "build1"), "1.3.18.6")
	if _, err := p.Install(context.Background(), platform.BuildOutput{Version: "1.3.18.6", AppPath: app}); err != nil {
		t.Fatal(err)
	}
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{RemovePrevious: true}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(p.DefaultInstallPath()); !os.IsNotExist(err) {
		t.Error("app should be gone after uninstall")
	}
}

func TestUninstallRefusesUnowned(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, &runner.FakeRunner{})
	p.InstallRoot = filepath.Join(dir, "Applications")
	target := p.DefaultInstallPath()
	if err := os.MkdirAll(filepath.Join(target, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := p.Uninstall(context.Background(), platform.UninstallOptions{})
	if !errors.Is(err, platform.ErrNotOwned) {
		t.Errorf("err = %v, want ErrNotOwned", err)
	}
	// Force still removes.
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{Force: true}); err != nil {
		t.Fatalf("forced uninstall: %v", err)
	}
}

func TestInstalledVersionNoInstall(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	got, err := p.InstalledVersion(context.Background())
	if err != nil || got != "" {
		t.Errorf("InstalledVersion = %q, %v; want empty,nil", got, err)
	}
}

func TestLaunchMissing(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	if err := p.Launch(context.Background()); err == nil {
		t.Error("launching a missing app should fail")
	}
}

func TestLaunchInvokesOpen(t *testing.T) {
	dir := t.TempDir()
	f := fakeVersionRunner("1.3.18.6")
	p := newTestPlatform(t, f)
	p.InstallRoot = filepath.Join(dir, "Applications")
	app := fakeApp(t, dir, "1.3.18.6")
	if _, err := p.Install(context.Background(), platform.BuildOutput{Version: "1.3.18.6", AppPath: app}); err != nil {
		t.Fatal(err)
	}
	f.Reset()
	if err := p.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	calls := f.Called()
	if len(calls) != 1 || calls[0].Name != "open" {
		t.Errorf("calls = %+v, want one open", calls)
	}
}
