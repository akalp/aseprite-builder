//go:build windows

package windows

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// fakeInstallPayload creates the bin directory an upstream Windows
// build produces (exe + data), without compiling anything.
func fakeInstallPayload(t *testing.T, root, version string) platform.BuildOutput {
	t.Helper()
	bin := filepath.Join(root, "build", "bin")
	if err := os.MkdirAll(filepath.Join(bin, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, exeName), []byte("PE32"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "data", "skin.zip"), []byte("data"), 0o644); err != nil {
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

func newTestPlatform(t *testing.T, r runner.Runner) Platform {
	t.Helper()
	root := t.TempDir()
	return Platform{
		Runner:         r,
		ProgramsRoot:   filepath.Join(root, "Programs"),
		StartMenuRoot:  filepath.Join(root, "StartMenu", "Programs"),
		BuilderVersion: "test",
		Now:            func() time.Time { return time.Unix(1700000000, 0) },
	}
}

// matchChain composes matcher funcs; the newer match wins first.
func appendMatch(a, b func(runner.Command) (runner.Result, bool)) func(runner.Command) (runner.Result, bool) {
	return func(c runner.Command) (runner.Result, bool) {
		if res, ok := b(c); ok {
			return res, true
		}
		if a != nil {
			return a(c)
		}
		return runner.Result{}, false
	}
}

func TestDetectWindows(t *testing.T) {
	f := &runner.FakeRunner{Results: map[string]runner.Result{
		"cmd /c ver": {Stdout: "\r\nMicrosoft Windows [Version 10.0.26100.3476]\r\n"},
	}}
	p := newTestPlatform(t, f)
	info, err := p.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.OS != "windows" || info.OSVersion != "10.0.26100" || info.Arch != platform.CurrentArch() {
		t.Errorf("info = %+v", info)
	}
	if !info.Supported {
		t.Error("windows amd64 must be a supported target")
	}
}

func TestParseWindowsVersionShapes(t *testing.T) {
	cases := map[string]string{
		"\r\nMicrosoft Windows [Version 10.0.26100.3476]\r\n": "10.0.26100",
		"Microsoft Windows [Version 10.0.22631.1]":            "10.0.22631",
		"garbage": "",
	}
	for in, want := range cases {
		if got := parseWindowsVersion(in); got != want {
			t.Errorf("parseWindowsVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequirementsDetectsToolchainFakes(t *testing.T) {
	// Fake host layout: vswhere under %ProgramFiles(x86)% answering
	// upstream build.cmd's two queries, and a Windows Kits SDK tree.
	vsroot := t.TempDir()
	t.Setenv("ProgramFiles(x86)", vsroot)
	vswhere := filepath.Join(vsroot, "Microsoft Visual Studio", "Installer", "vswhere.exe")
	if err := os.MkdirAll(filepath.Dir(vswhere), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vswhere, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	installPath := filepath.Join(vsroot, "BuildTools")
	vcvars := filepath.Join(installPath, "VC", "Auxiliary", "Build", "vcvars64.bat")
	f := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if filepath.Base(c.Name) == "vswhere.exe" && len(c.Args) > 0 {
			switch {
			case c.Args[0] == "-latest" && c.Args[len(c.Args)-1] == "installationPath":
				return runner.Result{Stdout: installPath + "\r\n"}, true
			case c.Args[0] == "-latest" && c.Args[len(c.Args)-1] == vcvarsRelPath:
				return runner.Result{Stdout: vcvars + "\r\n"}, true
			}
		}
		return runner.Result{}, false
	}}
	p := newTestPlatform(t, f)

	// SDK include tree with UM headers.
	kits := filepath.Join(vsroot, "Windows Kits", "10", "Include", "10.0.26100.0", "um")
	if err := os.MkdirAll(kits, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kits, "windows.h"), []byte("h"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Free-space probe: the fake runner answers the PowerShell query.
	f.Match = appendMatch(f.Match, func(c runner.Command) (runner.Result, bool) {
		if c.Name == "powershell" {
			return runner.Result{Stdout: "123456789012\n"}, true
		}
		return runner.Result{}, false
	})

	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	byName := map[string]platform.Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	for _, name := range []string{
		"Visual Studio Build Tools (C++ workload)",
		"MSVC environment (vcvars64.bat)",
		"Windows SDK",
		"Disk space",
	} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("missing check %q in %+v", name, byName)
		}
		if !c.OK {
			t.Errorf("check %q should be OK: %+v", name, c)
		}
	}
}

func TestRequirementsMissingToolchain(t *testing.T) {
	t.Setenv("ProgramFiles(x86)", t.TempDir()) // no vswhere inside
	p := newTestPlatform(t, &runner.FakeRunner{})
	checks, err := p.Requirements(context.Background())
	if err != nil {
		t.Fatalf("Requirements: %v", err)
	}
	var failing int
	for _, c := range checks {
		if !c.OK {
			failing++
			if c.Fix == "" {
				t.Errorf("failing check %q carries no fix hint", c.Name)
			}
		}
	}
	if failing < 3 {
		t.Errorf("host without VS essentials must fail at least 3 checks; failing = %d", failing)
	}
}

func TestBuildEnvironmentCapturesSetOutput(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if c.Name == "cmd" && len(c.Args) == 5 && c.Args[0] == "/d" && c.Args[1] == "/c" &&
			c.Args[2] == "call" && strings.HasSuffix(strings.ToLower(c.Args[3]), "vcvars64.bat") && c.Args[4] == "set" {
			return runner.Result{Stdout: "PATH=C:\\VS\\bin;C:\\Windows\r\nLIB=C:\\VS\\LIB;C:\\KIT\\Lib\r\nINCLUDE=C:\\KIT\\Include;C:\\VS\\INCLUDE\r\n\r\nfoo\r\n"}, true
		}
		return runner.Result{}, false
	}})
	env, err := p.BuildEnvironment(context.Background())
	if err != nil {
		t.Fatalf("BuildEnvironment: %v", err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"PATH=C:\\VS\\bin;C:\\Windows",
		"LIB=C:\\VS\\LIB;C:\\KIT\\Lib",
		"INCLUDE=C:\\KIT\\Include;C:\\VS\\INCLUDE",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %q: %q", want, joined)
		}
	}
	// Non-assignment lines are dropped.
	for _, e := range env {
		if !strings.Contains(e, "=") {
			t.Errorf("env entry %q is not KEY=VALUE", e)
		}
	}
}

func TestBuildEnvironmentFailure(t *testing.T) {
	dummy := t.TempDir()
	p := newTestPlatform(t, &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		return runner.Result{ExitCode: 3, Stderr: "bad batch\r\n"}, true
	}})
	_, err := p.BuildEnvironment(context.Background())
	if err == nil || !strings.Contains(err.Error(), "importing the MSVC environment") {
		t.Fatalf("err = %v, want the vcvars import failure", err)
	}
	_ = dummy
}

func TestInstallFresh(t *testing.T) {
	dir := t.TempDir()
	f := &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	p := newTestPlatform(t, f)
	out := fakeInstallPayload(t, dir, "1.3.18.6")

	outcome, err := p.Install(context.Background(), out)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	target := p.DefaultInstallPath()
	if outcome.AppPath != target {
		t.Errorf("AppPath = %q, want %q", outcome.AppPath, target)
	}
	if _, err := os.Stat(filepath.Join(target, exeName)); err != nil {
		t.Errorf("installed exe missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "skin.zip")); err != nil {
		t.Errorf("installed data missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, markerName)); err != nil {
		t.Errorf("ownership marker missing: %v", err)
	}
	if _, err := os.Stat(p.ShortcutPath()); err != nil {
		t.Errorf("Start Menu shortcut missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.ProgramsRoot, retentionDirName, "staging")); !os.IsNotExist(err) {
		t.Error("staging dir should be removed after success")
	}
	if outcome.PreviousPath != "" {
		t.Errorf("PreviousPath = %q, want empty on fresh install", outcome.PreviousPath)
	}
}

func TestInstallReplacesAndRetainsPrevious(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "Programs")

	p := newTestPlatform(t, &runner.FakeRunner{Match: versionFaker("1.3.18.5")})
	p.ProgramsRoot = root
	first := fakeInstallPayload(t, filepath.Join(dir, "b1"), "1.3.18.5")
	if _, err := p.Install(context.Background(), first); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	p.Runner = &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	second := fakeInstallPayload(t, filepath.Join(dir, "b2"), "1.3.18.6")
	outcome, err := p.Install(context.Background(), second)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if outcome.PreviousPath == "" {
		t.Fatal("previous installation should be retained")
	}
	m, err := readMarker(outcome.PreviousPath)
	if err != nil || m.Version != "1.3.18.5" {
		t.Errorf("retained previous = %+v (%v)", m, err)
	}
	got, err := p.InstalledVersion(context.Background())
	if err != nil || got != "1.3.18.6" {
		t.Errorf("InstalledVersion = %q %v, want 1.3.18.6", got, err)
	}
}

func TestInstallFailureKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, &runner.FakeRunner{Match: versionFaker("1.3.18.5")})
	p.ProgramsRoot = filepath.Join(dir, "Programs")
	first := fakeInstallPayload(t, filepath.Join(dir, "b1"), "1.3.18.5")
	if _, err := p.Install(context.Background(), first); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	// Break the second install at the staged-verification step: the exe
	// probe is injected as a failure.
	broken := fakeInstallPayload(t, filepath.Join(dir, "b2"), "1.3.18.6")
	stagedExe := filepath.Join(p.ProgramsRoot, retentionDirName, "staging", installDirName, exeName)
	f := &runner.FakeRunner{Match: versionFaker("1.3.18.5")}
	f.Errs = map[string]error{stagedExe + " --version": errors.New("simulated verify failure")}
	p.Runner = f
	if _, err := p.Install(context.Background(), broken); err == nil {
		t.Fatal("install failing verification must abort")
	}
	if got, _ := p.InstalledVersion(context.Background()); got != "1.3.18.5" {
		t.Errorf("InstalledVersion after failed update = %q, want 1.3.18.5 intact", got)
	}
}

func TestInstallRefusesUnownedTarget(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, &runner.FakeRunner{})
	p.ProgramsRoot = filepath.Join(dir, "Programs")

	target := p.DefaultInstallPath()
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	built := fakeInstallPayload(t, filepath.Join(dir, "b1"), "1.3.18.6")
	if _, err := p.Install(context.Background(), built); err == nil {
		t.Fatal("install must refuse to replace an unowned dir without --force")
	}
	if got, _ := p.InstalledVersion(context.Background()); got != "" {
		t.Error("unowned target must not gain a builder marker")
	}

	p.AllowReplaceUnowned = true
	p.Runner = &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	if _, err := p.Install(context.Background(), built); err != nil {
		t.Fatalf("forced install: %v", err)
	}
	if got, _ := p.InstalledVersion(context.Background()); got != "1.3.18.6" {
		t.Errorf("InstalledVersion = %q", got)
	}
}

func TestUninstallOwnedAndNotOwned(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, &runner.FakeRunner{Match: versionFaker("1.3.18.6")})
	p.ProgramsRoot = filepath.Join(dir, "Programs")

	app := fakeInstallPayload(t, filepath.Join(dir, "b1"), "1.3.18.6")
	if _, err := p.Install(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{RemovePrevious: true}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(p.DefaultInstallPath()); !os.IsNotExist(err) {
		t.Error("install dir should be gone after uninstall")
	}
	if _, err := os.Stat(p.ShortcutPath()); !os.IsNotExist(err) {
		t.Error("shortcut should be gone after uninstall")
	}
}

func TestUninstallRefusesUnowned(t *testing.T) {
	dir := t.TempDir()
	p := newTestPlatform(t, &runner.FakeRunner{})
	p.ProgramsRoot = filepath.Join(dir, "Programs")
	if err := os.MkdirAll(p.DefaultInstallPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{}); !errors.Is(err, platform.ErrNotOwned) {
		t.Errorf("err = %v, want ErrNotOwned", err)
	}
	if err := p.Uninstall(context.Background(), platform.UninstallOptions{Force: true}); err != nil {
		t.Errorf("forced uninstall: %v", err)
	}
}

func TestInstalledVersionNoInstall(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	got, err := p.InstalledVersion(context.Background())
	if err != nil || got != "" {
		t.Errorf("InstalledVersion = %q %v, want empty,nil", got, err)
	}
}

func TestLaunchMissing(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	if err := p.Launch(context.Background()); err == nil {
		t.Error("launching a missing installation should fail")
	}
}

func TestLaunchUsesStartDetached(t *testing.T) {
	dir := t.TempDir()
	f := &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	p := newTestPlatform(t, f)
	p.ProgramsRoot = filepath.Join(dir, "Programs")
	app := fakeInstallPayload(t, filepath.Join(dir, "b1"), "1.3.18.6")
	if _, err := p.Install(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	f.Reset()
	if err := p.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	calls := f.Called()
	if len(calls) != 1 || calls[0].Name != "cmd" {
		t.Fatalf("calls = %+v", calls)
	}
	joined := strings.Join(calls[0].Args, " ")
	if !strings.Contains(joined, "start") || !strings.Contains(joined, exeName) {
		t.Errorf("start args = %q", joined)
	}
}
