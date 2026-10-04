package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/state"
	"github.com/akalp/aseprite-builder/internal/upstream"
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
	if !strings.Contains(st.BuildContract, "level=2") {
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

// winFakePlatform models the Windows platform surface the service
// pipeline consumes: non-bundle install path, the VS build environment
// capability and the installed-executable capability.
type winFakePlatform struct {
	installPath string
	capturedEnv []string
	installed   string
}

func (f *winFakePlatform) Detect(context.Context) (platform.SystemInfo, error) {
	return platform.SystemInfo{OS: "windows", OSVersion: "10.0.26100", Arch: "amd64", Supported: true}, nil
}

func (f *winFakePlatform) Requirements(context.Context) ([]platform.Check, error) {
	return []platform.Check{{Name: "Visual Studio Build Tools", OK: true, Detail: "fake"}}, nil
}

func (f *winFakePlatform) Prepare(context.Context) error { return nil }

// BuildEnvironment implements app.BuildEnvironmentProvider: the
// captured MSVC environment the service must pass into the build.
func (f *winFakePlatform) BuildEnvironment(context.Context) ([]string, error) {
	f.capturedEnv = []string{
		"PATH=C:\\VS\\Common7\\IDE\\VC\\Tools\\MSVC\\14.44\\bin\\HostX64\\x64;C:\\Windows",
		"LIB=C:\\VS\\VC\\Tools\\MSVC\\14.44\\lib\\x64",
		"INCLUDE=C:\\VS\\VC\\Tools\\MSVC\\14.44\\include",
	}
	return f.capturedEnv, nil
}

func (f *winFakePlatform) Install(_ context.Context, out platform.BuildOutput) (platform.InstallOutcome, error) {
	f.installed = out.Version
	if out.BinDir == "" {
		return platform.InstallOutcome{}, errors.New("windows install needs BinDir")
	}
	return platform.InstallOutcome{AppPath: f.installPath, PreviousPath: ""}, nil
}

func (f *winFakePlatform) Launch(context.Context) error                               { return nil }
func (f *winFakePlatform) Uninstall(context.Context, platform.UninstallOptions) error { return nil }
func (f *winFakePlatform) InstalledVersion(context.Context) (string, error)           { return f.installed, nil }
func (f *winFakePlatform) DefaultInstallPath() string                                 { return f.installPath }

// InstalledExecutable implements platform.InstalledExecutableProvider.
func (f *winFakePlatform) InstalledExecutable() (string, error) {
	return filepath.Join(f.installPath, "aseprite.exe"), nil
}

func (f *winFakePlatform) SetAllowReplaceUnowned(bool) {}

// windowsSourceZip composes the official-source-shaped zip with the
// Windows contract markers (build.cmd/build.ps1, exe/data cmake files,
// and a real-shaped skia-url.sh for the bash-free discovery).
func windowsSourceZip(t *testing.T) []byte {
	t.Helper()
	urlSh := "#!/usr/bin/env bash\n" +
		"if [ $is_win ] ; then\n" +
		"    skia_file=Skia-Windows-$skia_build-$cpu.zip\n" +
		"elif [ $is_macos ] ; then\n" +
		"    skia_file=Skia-macOS-$skia_build-$cpu.zip\n" +
		"else\n" +
		"    skia_file=Skia-Linux-$skia_build-$cpu.zip\n" +
		"fi\n" +
		"echo https://github.com/aseprite/skia/releases/download/$skia_tag/$skia_file\n"
	files := map[string]string{
		"EULA.txt":                "license",
		".gitmodules":             "[submodule \"laf\"]",
		"build.sh":                "#!/usr/bin/env bash",
		"build.cmd":               "@echo off\r\n",
		"build.ps1":               "# windows wrapper\r\n",
		"CMakeLists.txt":          "cmake_minimum_required(VERSION 3.20)\nproject(aseprite C CXX)\n",
		"laf/CMakeLists.txt":      "# laf",
		"laf/clip/CMakeLists.txt": "# clip",
		"laf/misc/platform.sh":    "# platform",
		"laf/misc/skia-tag.txt":   "m124-08a5439a6b\n",
		"laf/misc/skia-url.sh":    urlSh,
		"src/CMakeLists.txt":      "add_executable(aseprite ${BUNDLE_PARAM})\nset(ORIGINAL_DATA_OUTPUT_DIR ${CMAKE_BINARY_DIR}/bin/data)\n",
	}
	return buildZipBytes(t, files)
}

// wireBuildFake_windows answers the two cmake invocations, the tool
// version probes and the produced exe's --version. The build step
// materializes the bin payload (aseprite.exe + data) the upstream
// Windows build would produce.
func wireBuildFake_windows(version string) func(runner.Command) (runner.Result, bool) {
	return func(c runner.Command) (runner.Result, bool) {
		if c.Name == "cmake" && len(c.Args) > 0 {
			switch c.Args[0] {
			case "-B":
				return runner.Result{ExitCode: 0}, true
			case "--build":
				srcDir := filepath.Dir(c.Args[1]) // argument: <src>/build
				bin := filepath.Join(srcDir, "build", "bin")
				if err := os.MkdirAll(filepath.Join(bin, "data"), 0o755); err != nil {
					return runner.Result{}, false
				}
				if err := os.WriteFile(filepath.Join(bin, "aseprite.exe"), []byte("PE"), 0o755); err != nil {
					return runner.Result{}, false
				}
				if err := os.WriteFile(filepath.Join(bin, "data", "skin.zip"), []byte("data"), 0o644); err != nil {
					return runner.Result{}, false
				}
				return runner.Result{ExitCode: 0}, true
			}
		}
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			switch filepath.Base(c.Name) {
			case "cmake":
				return runner.Result{Stdout: "cmake version 3.31.6\n"}, true
			case "ninja", "ninja.exe":
				return runner.Result{Stdout: "1.13.1\n"}, true
			}
			if strings.HasSuffix(c.Name, "aseprite.exe") {
				return runner.Result{Stdout: "Aseprite v" + version + "\n"}, true
			}
		}
		return runner.Result{}, false
	}
}

// releaseListJSON encodes a GitHub-shaped releases payload.
func releaseListJSON(t *testing.T, tag string, assets []map[string]string) []byte {
	t.Helper()
	type asset struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}
	type rel struct {
		TagName    string  `json:"tag_name"`
		Draft      bool    `json:"draft"`
		PreRelease bool    `json:"prerelease"`
		Assets     []asset `json:"assets"`
	}
	out := []rel{}
	if tag != "" {
		r := rel{TagName: tag}
		for _, a := range assets {
			r.Assets = append(r.Assets, asset{Name: a["name"], BrowserDownloadURL: a["browser_download_url"]})
		}
		out = append(out, r)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// binContents picks the canned archive for the download path.
func binContents(urlPath string, sourceZip, skiaZip, cmakeZip, ninjaZip []byte) []byte {
	switch path.Base(urlPath) {
	case "Skia-Windows-Release-x64.zip":
		return skiaZip
	case "cmake-4.1.2-windows-x86_64.zip":
		return cmakeZip
	case "ninja-win.zip":
		return ninjaZip
	default:
		return sourceZip
	}
}

func TestInstallWindowsShape(t *testing.T) {
	layout := mustLayout(t)
	plat := &winFakePlatform{installPath: `C:\Users\u\AppData\Local\Programs\Aseprite`}
	fk := &runner.FakeRunner{}
	fk.Match = wireBuildFake_windows("1.3.18.6")

	// upstreamSrv fakes both the GitHub-shaped API and the binary
	// downloads (CMake/Ninja/Skia) on one server.
	sourceZip := windowsSourceZip(t)
	skiaZip := buildZipBytes(t, map[string]string{
		"Skia-Windows-Release-x64/out/Release-x64/skia.lib":                        "LIB",
		"Skia-Windows-Release-x64/third_party/externals/libwebp/src/webp/decode.h": "w",
	})
	cmakeZip := buildZipBytes(t, map[string]string{
		"cmake-4.1.2-windows-x86_64/bin/cmake.exe": "PE",
	})
	ninjaZip := buildZipBytes(t, map[string]string{"ninja.exe": "PE"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/"):
			w.Header().Set("Content-Type", "application/json")
			host := "http://" + r.Host
			assetOf := func(name string) []map[string]string {
				return []map[string]string{
					{"name": name, "browser_download_url": host + "/" + name},
				}
			}
			var out []byte
			switch {
			case strings.Contains(r.URL.Path, "Kitware/CMake"):
				out = releaseListJSON(t, "v4.1.2", assetOf("cmake-4.1.2-windows-x86_64.zip"))
			case strings.Contains(r.URL.Path, "ninja-build/ninja"):
				out = releaseListJSON(t, "v1.13.1", assetOf("ninja-win.zip"))
			case strings.Contains(r.URL.Path, "aseprite/skia"):
				out = releaseListJSON(t, "m124-08a5439a6b", assetOf("Skia-Windows-Release-x64.zip"))
			default:
				out = releaseListJSON(t, "", nil)
			}
			_, _ = w.Write(out)
		default:
			_, _ = w.Write(binContents(r.URL.Path, sourceZip, skiaZip, cmakeZip, ninjaZip))
		}
	}))
	defer srv.Close()

	svc, err := New(Options{
		BuilderVersion:      "test",
		Layout:              layout,
		Platform:            plat,
		BuildRunnerOverride: fk,
		Releases: fakeReleases{releases: []aseprite.Release{{
			Version:   mustVer("1.3.18.6"),
			TagName:   "v1.3.18.6",
			SourceURL: "https://upstream.invalid/Aseprite-v1.3.18.6-Source.zip",
			AssetSize: int64(len(sourceZip)),
		}}},
		Upstream: &upstream.Client{BaseURL: srv.URL, UserAgent: "test"},
		Runner:   fk,
		Store:    state.Store{Dir: layout.State},
		fetchHook: func(ctx context.Context, src, dst, sha string, progress func(int64, int64)) error {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			return os.WriteFile(dst, sourceZip, 0o644)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var events []ProgressEvent
	result, err := svc.Install(context.Background(), InstallOptions{}, func(e ProgressEvent) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("Install (windows shape): %v", err)
	}

	// The Windows contract was inspected (os token) and the VS
	// environment captured and passed into the build.
	if !strings.Contains(result.Contract, "os=windows") {
		t.Errorf("Contract = %q, want the windows fingerprint", result.Contract)
	}
	if len(plat.capturedEnv) != 3 {
		t.Fatalf("captured env = %d entries, want the fake's 3", len(plat.capturedEnv))
	}
	var sawEnv []string
	for _, c := range fk.Called() {
		if c.Name == "cmake" && len(c.Args) > 0 && (c.Args[0] == "-B" || c.Args[0] == "--build") {
			sawEnv = c.Env
		}
	}
	for _, want := range plat.capturedEnv {
		if !containsEnv(sawEnv, want) {
			t.Errorf("build env missing %q (got %q)", want, sawEnv)
		}
	}
	// The configure step passes -DSKIA_LIBRARY_DIR with the x64 release
	// path amd64 maps to.
	var configure runner.Command
	for _, c := range fk.Called() {
		if c.Name == "cmake" && len(c.Args) > 0 && c.Args[0] == "-B" {
			configure = c
			break
		}
	}
	joined := strings.Join(configure.Args, " ")
	if !strings.Contains(joined, "Release-x64") {
		t.Errorf("configure args missing Release-x64: %s", joined)
	}
	if !strings.Contains(joined, "RelWithDebInfo") {
		t.Errorf("configure args missing RelWithDebInfo: %s", joined)
	}

	// Install reached the platform with Version and the state records
	// the windows contract.
	if plat.installed != "1.3.18.6" {
		t.Errorf("platform installed = %q", plat.installed)
	}
	st, ok, err := svc.CurrentInstall()
	if err != nil || !ok {
		t.Fatalf("CurrentInstall: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(st.BuildContract, "os=windows") {
		t.Errorf("state BuildContract = %q", st.BuildContract)
	}
}

func containsEnv(env []string, want string) bool {
	wantKey := strings.SplitN(want, "=", 2)[0]
	for _, e := range env {
		if strings.SplitN(e, "=", 2)[0] == wantKey && strings.Contains(e, strings.SplitN(want, "=", 2)[1]) {
			return true
		}
	}
	return false
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
