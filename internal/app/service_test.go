package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aseprite-builder/aseprite-builder/internal/aseprite"
	"github.com/aseprite-builder/aseprite-builder/internal/paths"
	"github.com/aseprite-builder/aseprite-builder/internal/platform"
	"github.com/aseprite-builder/aseprite-builder/internal/runner"
	"github.com/aseprite-builder/aseprite-builder/internal/state"
	"github.com/aseprite-builder/aseprite-builder/internal/upstream"
)

// fakePlatform implements platform.Platform for service tests without
// touching the real OS.
type fakePlatform struct {
	allowUnowned bool
	installed    string // version marker
	failInstall  bool
	launchCalls  int
}

func (f *fakePlatform) Detect(context.Context) (platform.SystemInfo, error) {
	return platform.SystemInfo{OS: "darwin", OSVersion: "15.2", Arch: "arm64", Supported: true}, nil
}

func (f *fakePlatform) Requirements(context.Context) ([]platform.Check, error) {
	return []platform.Check{
		{Name: "Toolchain", OK: true, Detail: "fake"},
		{Name: "Disk", OK: true, Detail: "plenty"},
	}, nil
}

func (f *fakePlatform) Prepare(context.Context) error { return nil }

func (f *fakePlatform) Install(_ context.Context, out platform.BuildOutput) (platform.InstallOutcome, error) {
	if f.failInstall {
		return platform.InstallOutcome{}, errors.New("simulated install failure")
	}
	f.installed = out.Version
	return platform.InstallOutcome{AppPath: out.AppPath}, nil
}

func (f *fakePlatform) Launch(context.Context) error { f.launchCalls++; return nil }

func (f *fakePlatform) Uninstall(context.Context, platform.UninstallOptions) error { return nil }

func (f *fakePlatform) InstalledVersion(context.Context) (string, error) { return f.installed, nil }

func (f *fakePlatform) DefaultInstallPath() string { return "/fake/Applications/Aseprite.app" }

func (f *fakePlatform) SetAllowReplaceUnowned(v bool) { f.allowUnowned = v }

// fakeReleases serves a fixed release list.
type fakeReleases struct {
	releases []aseprite.Release
}

func (f fakeReleases) Resolve(ctx context.Context, c aseprite.Channel) (aseprite.Release, error) {
	rels, _ := f.List(ctx, c)
	if len(rels) == 0 {
		return aseprite.Release{}, errors.New("no releases")
	}
	return rels[0], nil
}

func (f fakeReleases) List(context.Context, aseprite.Channel) ([]aseprite.Release, error) {
	return f.releases, nil
}

// fixtureSourceZip builds a zip mimicking the official Aseprite source
// archive layout (flat, with the contract markers). No Aseprite code
// compiles here: the entries are stand-ins.
func fixtureSourceZip(t *testing.T, version string) []byte {
	t.Helper()
	files := map[string]string{
		"EULA.txt":                "license",
		".gitmodules":             "[submodule \"laf\"]",
		"build.sh":                "#!/usr/bin/env bash\necho build",
		"CMakeLists.txt":          "cmake_minimum_required(VERSION 3.20)\nproject(aseprite C CXX)\n",
		"laf/CMakeLists.txt":      "# laf",
		"laf/clip/CMakeLists.txt": "# clip",
		"laf/misc/platform.sh":    "platform",
		"laf/misc/skia-tag.txt":   "m124-08a5439a6b\n",
		"laf/misc/skia-url.sh":    "url",
		"src/main/osx/Info.plist": "<plist/>",
		"src/CMakeLists.txt":      "${CMAKE_BINARY_DIR}/bin/Aseprite.app/Contents/Resources/data\n",
	}
	end := filepath.Join(t.TempDir(), "src.zip")
	f, err := os.Create(end)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fo, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fo.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(end)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// newTestService wires a Service with all fakes.
func newTestService(t *testing.T) (*Service, *fakePlatform, *runner.FakeRunner, string) {
	t.Helper()
	layout, err := paths.New(t.TempDir())
	if err != nil {
		t.Fatalf("paths.New: %v", err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	plat := &fakePlatform{}
	fk := &runner.FakeRunner{}

	sourceZip := fixtureSourceZip(t, "1.3.18.6")
	svc, err := New(Options{
		BuilderVersion:      "test",
		Layout:              layout,
		Platform:            plat,
		BuildRunnerOverride: fk,
		Releases: fakeReleases{releases: []aseprite.Release{{
			Version:   mustVer("1.3.18.6"),
			TagName:   "v1.3.18.6",
			SourceURL: "https://upstream.invalid/Aseprite-v1.3.18.6-Source.zip",
			SHA256:    "",
			AssetSize: int64(len(sourceZip)),
		}}},
		Runner: fk,
		Store:  state.Store{Dir: layout.State},
		fetchHook: func(ctx context.Context, src, dst, sha string, progress func(int64, int64)) error {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			return os.WriteFile(dst, sourceZip, 0o644)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, plat, fk, layout.Sources
}

func mustVer(s string) aseprite.Version {
	v, _ := aseprite.ParseVersion(s)
	return v
}

// wireBuildFake emulates the upstream build: when build.sh "runs", the
// test materializes the Aseprite.app bundle the upstream build would
// produce. The --version probe of the produced executable succeeds too.
func wireBuildFake(t *testing.T, fk *runner.FakeRunner, sourcesDir string, version string) {
	t.Helper()
	fk.Match = func(c runner.Command) (runner.Result, bool) {
		// Upstream build.sh invocation
		if c.Name == "bash" && len(c.Args) == 3 && strings.HasSuffix(c.Args[0], "build.sh") {
			srcDir := filepath.Dir(c.Args[0])
			if err := materializeBuildOutput(t, srcDir, version); err != nil {
				return runner.Result{}, false
			}
			return runner.Result{ExitCode: 0}, true
		}
		// System tool probes report compatible versions.
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			switch filepath.Base(c.Name) {
			case "cmake":
				return runner.Result{Stdout: "cmake version 3.31.6\n"}, true
			case "ninja":
				return runner.Result{Stdout: "1.13.1\n"}, true
			}
		}
		// Upstream skia-url.sh resolver.
		if c.Name == "bash" && len(c.Args) == 4 && strings.HasSuffix(c.Args[0], "skia-url.sh") {
			return runner.Result{Stdout: "https://github.com/aseprite/skia/releases/download/m124-08a5439a6b/Skia-macOS-Release-arm64.zip\n"}, true
		}
		// The produced executable's --version
		if len(c.Args) == 1 && c.Args[0] == "--version" && strings.HasSuffix(c.Name, "aseprite") {
			return runner.Result{Stdout: "Aseprite v" + version + "\n"}, true
		}
		return runner.Result{}, false
	}
}

// materializeBuildOutput creates the fixture the upstream mac build
// produces (bundle + data), without compiling anything.
func materializeBuildOutput(t *testing.T, srcDir, version string) error {
	t.Helper()
	app := filepath.Join(srcDir, "build", "bin", "Aseprite.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(app, "Contents", "Resources", "data"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "aseprite"), []byte("macho"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(app, "Contents", "Resources", "data", "skin.zip"), []byte("data"), 0o644)
}

func TestInstallHappyPath(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")

	var events []ProgressEvent
	result, err := svc.Install(context.Background(), InstallOptions{}, func(e ProgressEvent) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Version != "1.3.18.6" {
		t.Errorf("Version = %q", result.Version)
	}
	if !strings.Contains(result.Contract, "cmake-min=3.20") {
		t.Errorf("Contract = %q", result.Contract)
	}
	if result.SkiaTag != "m124-08a5439a6b" {
		t.Errorf("SkiaTag = %q", result.SkiaTag)
	}
	if plat.installed != "1.3.18.6" {
		t.Errorf("platform installed = %q", plat.installed)
	}

	// All expected stages emitted in order.
	wantOrder := []Stage{
		StageDetect, StageSystemRequirements, StageResolveRelease, StageFetchSource,
		StageInspectContract, StageResolveTools, StageResolveSkia, StageBuild,
		StageVerify, StageInstall, StageCleanup,
	}
	if len(events) < len(wantOrder) {
		t.Fatalf("got %d events, want at least %d", len(events), len(wantOrder))
	}
	idx := 0
	for _, e := range events {
		if e.Stage == wantOrder[idx] {
			idx++
		}
		if idx == len(wantOrder) {
			break
		}
	}
	if idx != len(wantOrder) {
		t.Errorf("stage order incomplete: got %d of %d stages", idx, len(wantOrder))
	}

	// State persisted.
	st, ok, err := svc.CurrentInstall()
	if err != nil || !ok {
		t.Fatalf("CurrentInstall: ok=%v err=%v", ok, err)
	}
	if st.AsepriteVersion != "1.3.18.6" || st.SkiaTag != "m124-08a5439a6b" {
		t.Errorf("state = %+v", st)
	}
	if !strings.Contains(st.BuildContract, "level=1") {
		t.Errorf("BuildContract = %q", st.BuildContract)
	}
}

func TestInstallSystemRequirementsFailSafe(t *testing.T) {
	svc, _, fk, _ := newTestService(t)
	wireBuildFake(t, fk, "", "1.3.18.6")
	// Replace platform with one that fails requirements.
	layout := mustLayout(t)
	failPlat := &failReqPlatform{}
	svc2, err := New(Options{
		BuilderVersion: "test", Layout: layout, Platform: failPlat,
		Releases: svc.opts.Releases, Runner: fk,
		Store: state.Store{Dir: layout.State},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc2.Install(context.Background(), InstallOptions{}, nil)
	if err == nil {
		t.Fatal("failing requirements must abort install")
	}
	var sre SystemRequirementsError
	if !errors.As(err, &sre) {
		t.Errorf("err = %T %v, want SystemRequirementsError", err, err)
	}
	if !errors.Is(err, ErrSystemRequirements) {
		t.Error("error should unwrap to ErrSystemRequirements")
	}
	// Nothing was installed or persisted.
	if _, has, _ := svc2.CurrentInstall(); has {
		t.Error("no install state must exist after failed requirements")
	}
}

type failReqPlatform struct{ fakePlatform }

func (failReqPlatform) Requirements(context.Context) ([]platform.Check, error) {
	return []platform.Check{{Name: "Xcode", OK: false, Fix: "xcode-select --install"}}, nil
}

func TestInstallUnknownContractFailsSafely(t *testing.T) {
	layout := mustLayout(t)
	fk := &runner.FakeRunner{}
	plat := &fakePlatform{}

	// The "downloaded" source zip is NOT a real Aseprite source.
	bogusZip := buildZipBytes(t, map[string]string{"random.txt": "nope"})
	svc, err := New(Options{
		BuilderVersion: "test", Layout: layout, Platform: plat,
		Releases: fakeReleases{releases: []aseprite.Release{{
			Version: mustVer("9.9.9"), TagName: "v9.9.9", SourceURL: "https://x/bogus.zip",
		}}},
		Runner: fk,
		Store:  state.Store{Dir: layout.State},
		fetchHook: func(ctx context.Context, src, dst, sha string, progress func(int64, int64)) error {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			return os.WriteFile(dst, bogusZip, 0o644)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Install(context.Background(), InstallOptions{}, nil)
	var uce UnknownContractError
	if !errors.As(err, &uce) {
		t.Fatalf("err = %T %v, want UnknownContractError", err, err)
	}
	if !errors.Is(err, ErrUnknownContract) {
		t.Error("should unwrap to ErrUnknownContract")
	}
	if _, has, _ := svc.CurrentInstall(); has {
		t.Error("failed contract inspection must not persist install state")
	}
}

func TestUpdateUpToDate(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	if _, err := svc.Install(context.Background(), InstallOptions{}, nil); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	_, err := svc.Update(context.Background(), InstallOptions{}, nil)
	if !errors.Is(err, ErrUpToDate) {
		t.Errorf("err = %v, want ErrUpToDate", err)
	}
}

func TestUpdateInstallsNewerVersion(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	if _, err := svc.Install(context.Background(), InstallOptions{}, nil); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	// Now the release list suddenly has a newer stable release.
	svc.opts.Releases = fakeReleases{releases: []aseprite.Release{
		{Version: mustVer("1.3.19"), TagName: "v1.3.19", SourceURL: "https://x/new.zip"},
		{Version: mustVer("1.3.18.6"), TagName: "v1.3.18.6", SourceURL: "https://x/old.zip"},
	}}
	wireBuildFake(t, fk, sourcesDir, "1.3.19")
	result, err := svc.Update(context.Background(), InstallOptions{}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Version != "1.3.19" {
		t.Errorf("updated version = %q", result.Version)
	}
	st, ok, _ := svc.CurrentInstall()
	if !ok || st.AsepriteVersion != "1.3.19" {
		t.Errorf("state after update = %+v ok=%v", st, ok)
	}
}

// TestInstallReusesExtractedSourceStillInspectsContract reproduces the
// rebuild failure: the source tree already exists (like after a first
// install), so provisioning takes the reuse path — but it must still
// run the contract inspection and derive SkiaDirName, otherwise build
// seeding breaks.
func TestInstallReusesExtractedSourceStillInspectsContract(t *testing.T) {
	layout := mustLayout(t)
	plat := &fakePlatform{}
	rSrc := &runner.FakeRunner{}

	// Materialize a source tree the way the first install would have.
	// (The fake build then produces output inside it as usual.)
	svc, err := New(Options{
		BuilderVersion:      "test",
		Layout:              layout,
		Platform:            plat,
		BuildRunnerOverride: rSrc,
		Releases: fakeReleases{releases: []aseprite.Release{{
			Version: mustVer("1.3.18.6"), TagName: "v1.3.18.6", SourceURL: "https://x/src.zip",
		}}},
		Runner: rSrc,
		Store:  state.Store{Dir: layout.State},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := layout.SourceTreeDir("1.3.18.6")
	writeSourceFixture(t, sourceDir) // full contract-compliant tree, no download

	wireBuildFakeForSource(t, rSrc, "1.3.18.6")
	result, err := svc.Install(context.Background(), InstallOptions{ForceOverwriteUnowned: true}, nil)
	if err != nil {
		t.Fatalf("Install on reused source: %v", err)
	}
	if result.SkiaTag != "m124-08a5439a6b" {
		t.Errorf("SkiaTag = %q (contract must be inspected on reuse)", result.SkiaTag)
	}
	// The Skia symlink must exist at the exact path upstream expects.
	if _, err := os.Readlink(filepath.Join(sourceDir, ".deps", "skia-m124")); err != nil {
		t.Errorf("skia symlink missing: %v", err)
	}
}

// TestInstallReusedCorruptSourceFailsSafely: a reused tree missing part
// of the upstream contract (simulates drift/accidental tampering) must
// fail inspection even when present from an earlier build.
func TestInstallReusedCorruptSourceFailsSafely(t *testing.T) {
	layout := mustLayout(t)
	plat := &fakePlatform{}
	rSrc := &runner.FakeRunner{}

	svc, err := New(Options{
		BuilderVersion:      "test",
		Layout:              layout,
		Platform:            plat,
		BuildRunnerOverride: rSrc,
		Releases: fakeReleases{releases: []aseprite.Release{{
			Version: mustVer("1.3.18.6"), TagName: "v1.3.18.6", SourceURL: "https://x/src.zip",
		}}},
		Runner: rSrc,
		Store:  state.Store{Dir: layout.State},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := layout.SourceTreeDir("1.3.18.6")
	writeSourceFixture(t, sourceDir)
	// Corrupt: remove a contract-critical file.
	if err := os.Remove(filepath.Join(sourceDir, "laf", "misc", "skia-url.sh")); err != nil {
		t.Fatal(err)
	}

	_, err = svc.Install(context.Background(), InstallOptions{}, nil)
	if !errors.Is(err, ErrUnknownContract) {
		t.Fatalf("err = %v, want unknown-contract on reused corrupt source", err)
	}
}

// writeSourceFixture writes a contract-compliant source tree (same
// shape as fixtureSourceZip) directly into dir.
func writeSourceFixture(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"EULA.txt":                "license",
		".gitmodules":             "[submodule \"laf\"]",
		"build.sh":                "#!/usr/bin/env bash\necho build",
		"CMakeLists.txt":          "cmake_minimum_required(VERSION 3.20)\nproject(aseprite C CXX)\n",
		"laf/CMakeLists.txt":      "# laf",
		"laf/clip/CMakeLists.txt": "# clip",
		"laf/misc/platform.sh":    "platform",
		"laf/misc/skia-tag.txt":   "m124-08a5439a6b\n",
		"laf/misc/skia-url.sh":    "url",
		"src/main/osx/Info.plist": "<plist/>",
		"src/CMakeLists.txt":      "${CMAKE_BINARY_DIR}/bin/Aseprite.app/Contents/Resources/data\n",
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// wireBuildFakeForSource emulates the upstream build for any source dir.
func wireBuildFakeForSource(t *testing.T, fk *runner.FakeRunner, version string) {
	t.Helper()
	fk.Match = func(c runner.Command) (runner.Result, bool) {
		if c.Name == "bash" && len(c.Args) == 3 && strings.HasSuffix(c.Args[0], "build.sh") {
			srcDir := filepath.Dir(c.Args[0])
			if err := materializeBuildOutput(t, srcDir, version); err != nil {
				return runner.Result{}, false
			}
			return runner.Result{ExitCode: 0}, true
		}
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			switch {
			case filepath.Base(c.Name) == "cmake":
				return runner.Result{Stdout: "cmake version 3.31.6\n"}, true
			case filepath.Base(c.Name) == "ninja":
				return runner.Result{Stdout: "1.13.1\n"}, true
			case strings.HasSuffix(c.Name, "aseprite"):
				return runner.Result{Stdout: "Aseprite v" + version + "\n"}, true
			}
		}
		if c.Name == "bash" && len(c.Args) == 4 && strings.HasSuffix(c.Args[0], "skia-url.sh") {
			return runner.Result{Stdout: "https://github.com/aseprite/skia/releases/download/m124-08a5439a6b/Skia-macOS-Release-arm64.zip\n"}, true
		}
		return runner.Result{}, false
	}
}

func TestLaunchAndUninstallLifecycle(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	if _, err := svc.Install(context.Background(), InstallOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if plat.launchCalls != 1 {
		t.Errorf("launchCalls = %d", plat.launchCalls)
	}
	if err := svc.Uninstall(context.Background(), UninstallOptions{RemovePrevious: true}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, has, _ := svc.CurrentInstall(); has {
		t.Error("install state must be cleared")
	}
}

func TestUninstallWithoutInstall(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if err := svc.Uninstall(context.Background(), UninstallOptions{}); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("err = %v, want ErrNotInstalled", err)
	}
}

func TestDoctorReport(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	report, err := svc.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.System.OS != "darwin" || report.System.Arch != "arm64" {
		t.Errorf("System = %+v", report.System)
	}
	if len(report.Requirements) == 0 {
		t.Error("requirements missing")
	}
	// JSON marshalling must work for doctor --json.
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"system"`) {
		t.Errorf("json = %s", string(data))
	}
}

// buildZipBytes writes an in-memory zip from files.
func buildZipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	end := filepath.Join(t.TempDir(), "b.zip")
	f, err := os.Create(end)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fo, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if content != "" {
			if _, err := fo.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(end)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustLayout(t *testing.T) paths.Layout {
	t.Helper()
	layout, err := paths.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	return layout
}

var _ = fmt.Sprintf
var upstreamClientIsUsed = upstream.New
