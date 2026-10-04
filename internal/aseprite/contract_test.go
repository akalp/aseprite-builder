package aseprite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// fixtureSource creates a minimal source tree with the structural
// markers the inspector requires. It models the layout of the official
// Aseprite-vX-Source.zip (flat, no wrapper dir).
func fixtureSource(t *testing.T, opts ...func(files map[string]string)) string {
	t.Helper()
	files := map[string]string{
		"EULA.txt":                "license",
		".gitmodules":             "[submodule \"laf\"]\n\tpath = laf\n",
		"build.sh":                "#!/usr/bin/env bash\necho build\n",
		"CMakeLists.txt":          "cmake_minimum_required(VERSION 3.20)\nproject(aseprite C CXX)\n",
		"laf/CMakeLists.txt":      "# laf",
		"laf/clip/CMakeLists.txt": "# clip",
		"laf/misc/platform.sh":    "# platform detection",
		"laf/misc/skia-tag.txt":   "m124-08a5439a6b\n",
		"laf/misc/skia-url.sh": "#!/usr/bin/env bash\n" +
			"if [ $is_win ] ; then\n" +
			"    skia_file=Skia-Windows-$skia_build-$cpu.zip\n" +
			"elif [ $is_macos ] ; then\n" +
			"    skia_file=Skia-macOS-$skia_build-$cpu.zip\n" +
			"else\n" +
			"    skia_file=Skia-Linux-$skia_build-$cpu.zip\n" +
			"fi\n" +
			"echo https://github.com/aseprite/skia/releases/download/$skia_tag/$skia_file\n",
		"src/main/osx/Info.plist":            "<plist>",
		"src/CMakeLists.txt":                 "# output contract\n# ${CMAKE_BINARY_DIR}/bin/Aseprite.app/Contents/Resources/data\n",
		"src/desktop/linux/aseprite.desktop": "[Desktop Entry]\nExec=aseprite %U\n",
		"build.cmd":                          "@echo off\r\nrem upstream windows entry point\r\n",
		"build.ps1":                          "# upstream windows wrapper\r\n",
	}
	for _, fn := range opts {
		fn(files)
	}
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestInspectRecognizesContract(t *testing.T) {
	src := fixtureSource(t)
	c, err := NewInspector().Inspect(src, ExeOutputAppBundle)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if c.CMakeMinVersion.String() != "3.20" {
		t.Errorf("CMakeMinVersion = %q, want 3.20", c.CMakeMinVersion)
	}
	if c.SkiaTag != "m124-08a5439a6b" {
		t.Errorf("SkiaTag = %q", c.SkiaTag)
	}
	if c.SkiaDirName != "skia-m124" {
		t.Errorf("SkiaDirName = %q, want skia-m124", c.SkiaDirName)
	}
	if c.BundleOutput == "" {
		t.Error("BundleOutput should be set for the macOS contract")
	}
	if c.Level != 2 {
		t.Errorf("Level = %d, want 2", c.Level)
	}
	if c.OS != "darwin" {
		t.Errorf("OS = %q, want darwin", c.OS)
	}
	if !strings.Contains(c.ContractFingerprint(), "cmake-min=3.20") {
		t.Errorf("fingerprint = %q", c.ContractFingerprint())
	}
	if !strings.Contains(c.ContractFingerprint(), "os=darwin") {
		t.Errorf("fingerprint = %q, want the os token", c.ContractFingerprint())
	}
}

func TestInspectMissingSkiaTag(t *testing.T) {
	src := fixtureSource(t, func(files map[string]string) {
		delete(files, "laf/misc/skia-tag.txt")
	})
	_, err := NewInspector().Inspect(src, ExeOutputAppBundle)
	var unknown UnknownContractError
	if err == nil {
		t.Fatal("missing skia-tag.txt must be an unknown contract")
	}
	if !strings.Contains(err.Error(), "skia-tag.txt") {
		t.Errorf("err = %v", err)
	}
	if !errorsAs(err, &unknown) {
		t.Errorf("err type = %T, want UnknownContractError", err)
	}
}

func TestInspectIncompleteSource(t *testing.T) {
	src := fixtureSource(t, func(files map[string]string) {
		delete(files, "laf/CMakeLists.txt")
	})
	_, err := NewInspector().Inspect(src, ExeOutputAppBundle)
	if err == nil || !strings.Contains(err.Error(), "laf/CMakeLists.txt") {
		t.Errorf("err = %v, want missing laf/CMakeLists.txt", err)
	}
}

func TestInspectNonDarwinIgnoresPlist(t *testing.T) {
	// The plain output configs (Windows/Linux) model platforms without
	// a bundle output contract. The plist is not part of them.
	src := fixtureSource(t, func(files map[string]string) {
		delete(files, "src/main/osx/Info.plist")
		files["src/CMakeLists.txt"] = windowsExeDataCmake
	})
	c, err := NewInspector().Inspect(src, ExeOutputWindows)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if c.BundleOutput != "" {
		t.Errorf("BundleOutput = %q, want empty", c.BundleOutput)
	}
}

func TestInspectCMakeVersionUndiscoverable(t *testing.T) {
	src := fixtureSource(t, func(files map[string]string) {
		files["CMakeLists.txt"] = "project(aseprite C CXX)\n"
	})
	_, err := NewInspector().Inspect(src, ExeOutputAppBundle)
	if err == nil || !strings.Contains(err.Error(), "CMake version") {
		t.Errorf("err = %v", err)
	}
}

func TestInspectMissingProject(t *testing.T) {
	src := fixtureSource(t, func(files map[string]string) {
		files["CMakeLists.txt"] = "cmake_minimum_required(VERSION 3.20)\n"
	})
	_, err := NewInspector().Inspect(src, ExeOutputAppBundle)
	if err == nil || !strings.Contains(err.Error(), "project(aseprite") {
		t.Errorf("err = %v", err)
	}
}

func TestSkiaDirNameFromTag(t *testing.T) {
	tests := map[string]string{
		"m124-08a5439a6b": "skia-m124",
		"m125":            "skia-m125",
		"m130-abc":        "skia-m130",
	}
	for tag, want := range tests {
		if got := skiaDirNameFromTag(tag); got != want {
			t.Errorf("skiaDirNameFromTag(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestParseSourceZipName(t *testing.T) {
	v, err := ParseSourceZipName("Aseprite-v1.3.18.6-Source.zip")
	if err != nil {
		t.Fatalf("ParseSourceZipName: %v", err)
	}
	if v.String() != "1.3.18.6" {
		t.Errorf("version = %q", v)
	}
	if _, err := ParseSourceZipName("Aseprite-v1.3.18-beta3-Source.zip"); err == nil {
		t.Error("beta zips must not parse as stable versions")
	}
	if _, err := ParseSourceZipName("skia.zip"); err == nil {
		t.Error("foreign assets must not parse")
	}
}

func TestDiscoverSkiaRequirement(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if c.Name == "bash" && len(c.Args) == 4 && strings.HasSuffix(c.Args[0], "skia-url.sh") {
			return runner.Result{Stdout: "https://github.com/aseprite/skia/releases/download/m124-08a5439a6b/Skia-macOS-Release-arm64.zip\n"}, true
		}
		return runner.Result{}, false
	}}
	req, err := DiscoverSkiaRequirement(context.Background(), r, src, SkiaPlatform{Name: "macos", CPU: "arm64"})
	if err != nil {
		t.Fatalf("DiscoverSkiaRequirement: %v", err)
	}
	if req.Tag != "m124-08a5439a6b" || req.DirName != "skia-m124" || req.CPU != "arm64" {
		t.Errorf("req = %+v", req)
	}
	wantURL := "https://github.com/aseprite/skia/releases/download/m124-08a5439a6b/Skia-macOS-Release-arm64.zip"
	if req.URL != wantURL {
		t.Errorf("URL = %q, want %q", req.URL, wantURL)
	}
	// The upstream script must have been invoked with our parameters.
	calls := r.Called()
	if len(calls) != 1 || calls[0].Name != "bash" || calls[0].Dir != src {
		t.Fatalf("calls = %+v", calls)
	}
	if got := strings.Join(calls[0].Args, " "); got != filepath.Join(src, "laf", "misc", "skia-url.sh")+" Release macos arm64" {
		t.Errorf("args = %q", got)
	}
}

func TestDiscoverSkiaRequirementRejectsForeignHost(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		return runner.Result{Stdout: "https://evil.example/skia.zip\n"}, true
	}}
	_, err := DiscoverSkiaRequirement(context.Background(), r, src, SkiaPlatform{Name: "macos", CPU: "arm64"})
	if err == nil || !strings.Contains(err.Error(), "github.com") {
		t.Errorf("err = %v", err)
	}
}

func TestDiscoverSkiaRequirementRejectsTagMismatch(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		return runner.Result{Stdout: "https://github.com/aseprite/skia/releases/download/m999-xxx/Skia-macOS-Release-arm64.zip\n"}, true
	}}
	_, err := DiscoverSkiaRequirement(context.Background(), r, src, SkiaPlatform{Name: "macos", CPU: "arm64"})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("err = %v", err)
	}
}

func TestDiscoverSkiaRequirementRejectsNonHTTPS(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		return runner.Result{Stdout: "http://github.com/aseprite/skia/releases/download/m124-08a5439a6b/Skia-macOS-Release-arm64.zip\n"}, true
	}}
	_, err := DiscoverSkiaRequirement(context.Background(), r, src, SkiaPlatform{Name: "macos", CPU: "arm64"})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("err = %v", err)
	}
}

func TestDiscoverSkiaRequirementScriptFailure(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Errs: map[string]error{"bash": context.DeadlineExceeded}}
	_, err := DiscoverSkiaRequirement(context.Background(), r, src, SkiaPlatform{Name: "macos", CPU: "arm64"})
	if err == nil {
		t.Fatal("script failure must propagate")
	}
}

// windowsExeDataCmake mirrors the real src/CMakeLists.txt Windows
// output contract shape (add_executable + bin/data copy target).
const windowsExeDataCmake = "add_executable(aseprite ${BUNDLE_PARAM}\n" +
	"set(ORIGINAL_DATA_OUTPUT_DIR ${CMAKE_BINARY_DIR}/bin/data)\n"

func TestInspectWindowsContract(t *testing.T) {
	src := fixtureSource(t, func(files map[string]string) {
		files["src/CMakeLists.txt"] = windowsExeDataCmake
	})
	c, err := NewInspector().Inspect(src, ExeOutputWindows)
	if err != nil {
		t.Fatalf("Inspect windows: %v", err)
	}
	if c.OS != "windows" {
		t.Errorf("OS = %q, want windows", c.OS)
	}
	if c.BundleOutput != "" {
		t.Errorf("BundleOutput = %q, want empty for the exe contract", c.BundleOutput)
	}
	if c.Level != 2 {
		t.Errorf("Level = %d, want 2", c.Level)
	}
	if c.CMakeMinVersion.String() != "3.20" || c.SkiaTag != "m124-08a5439a6b" {
		t.Errorf("contract = %+v", c)
	}
	if !strings.Contains(c.ContractFingerprint(), "os=windows") {
		t.Errorf("fingerprint = %q", c.ContractFingerprint())
	}
}

func TestInspectWindowsMissingEntryPoints(t *testing.T) {
	for _, gone := range []string{"build.cmd", "build.ps1"} {
		src := fixtureSource(t, func(files map[string]string) {
			files["src/CMakeLists.txt"] = windowsExeDataCmake
			delete(files, gone)
		})
		_, err := NewInspector().Inspect(src, ExeOutputWindows)
		if err == nil || !strings.Contains(err.Error(), gone) {
			t.Errorf("missing %s must fail inspection; err = %v", gone, err)
		}
	}
}

func TestInspectWindowsMutatedOutputContract(t *testing.T) {
	// Upstream drops the exe/bin-data markers: fail safely.
	src := fixtureSource(t, func(files map[string]string) {
		files["src/CMakeLists.txt"] = "# no output rules anymore\n"
	})
	_, err := NewInspector().Inspect(src, ExeOutputWindows)
	var unknown UnknownContractError
	if err == nil || !strings.Contains(err.Error(), "Windows exe + bin/data") {
		t.Fatalf("err = %v, want the windows output-contract failure", err)
	}
	if !errorsAs(err, &unknown) {
		t.Errorf("err type = %T, want UnknownContractError", err)
	}
}

func TestInspectWindowsUndiscoverableCMakeVersion(t *testing.T) {
	src := fixtureSource(t, func(files map[string]string) {
		files["CMakeLists.txt"] = "project(aseprite C CXX)\n"
		files["src/CMakeLists.txt"] = windowsExeDataCmake
	})
	_, err := NewInspector().Inspect(src, ExeOutputWindows)
	if err == nil || !strings.Contains(err.Error(), "CMake version") {
		t.Errorf("err = %v", err)
	}
}

func TestInspectLinuxContract(t *testing.T) {
	// Linux has no Windows entry-point markers and no macOS plist: the
	// same source shape as macOS, consumed through build.sh, with the
	// plain executable + bin/data output contract.
	src := fixtureSource(t, func(files map[string]string) {
		files["src/CMakeLists.txt"] = windowsExeDataCmake
		delete(files, "build.cmd")
		delete(files, "build.ps1")
		delete(files, "src/main/osx/Info.plist")
	})
	c, err := NewInspector().Inspect(src, ExeOutputLinux)
	if err != nil {
		t.Fatalf("Inspect linux: %v", err)
	}
	if c.OS != "linux" {
		t.Errorf("OS = %q, want linux", c.OS)
	}
	if c.BundleOutput != "" {
		t.Errorf("BundleOutput = %q, want empty for the executable contract", c.BundleOutput)
	}
	if c.Level != 2 {
		t.Errorf("Level = %d, want 2", c.Level)
	}
	if c.BuildScript != filepath.Join(src, "build.sh") {
		t.Errorf("BuildScript = %q, want <src>/build.sh", c.BuildScript)
	}
	if !strings.Contains(c.ContractFingerprint(), "os=linux") {
		t.Errorf("fingerprint = %q, want the os=linux token", c.ContractFingerprint())
	}
}

func TestInspectLinuxMutatedOutputContract(t *testing.T) {
	// Upstream drops the executable/bin-data markers: fail safely with
	// the Linux contract named.
	src := fixtureSource(t, func(files map[string]string) {
		files["src/CMakeLists.txt"] = "# no output rules anymore\n"
	})
	_, err := NewInspector().Inspect(src, ExeOutputLinux)
	var unknown UnknownContractError
	if err == nil || !strings.Contains(err.Error(), "Linux executable + bin/data") {
		t.Fatalf("err = %v, want the linux output-contract failure", err)
	}
	if !errorsAs(err, &unknown) {
		t.Errorf("err type = %T, want UnknownContractError", err)
	}
}

func TestInspectLinuxMissingBuildScript(t *testing.T) {
	// build.sh is the Linux build entry point; its absence must fail
	// inspection rather than fall through to an unknown flow.
	src := fixtureSource(t, func(files map[string]string) {
		files["src/CMakeLists.txt"] = windowsExeDataCmake
		delete(files, "build.sh")
	})
	_, err := NewInspector().Inspect(src, ExeOutputLinux)
	if err == nil || !strings.Contains(err.Error(), "build.sh") {
		t.Errorf("err = %v, want missing build.sh", err)
	}
}

func TestExeOutputConfigOS(t *testing.T) {
	if got := ExeOutputAppBundle.OS(); got != "darwin" {
		t.Errorf("bundle OS = %q", got)
	}
	if got := ExeOutputWindows.OS(); got != "windows" {
		t.Errorf("windows OS = %q", got)
	}
	if got := ExeOutputLinux.OS(); got != "linux" {
		t.Errorf("linux OS = %q", got)
	}
	if got := ExeOutputConfig(0).OS(); got != "" {
		t.Errorf("zero config OS = %q, want empty", got)
	}
}

func TestDiscoverSkiaRequirementBashFree(t *testing.T) {
	src := fixtureSource(t)
	req, err := DiscoverSkiaRequirementBashFree(src, SkiaPlatform{Name: "windows", CPU: "x64"})
	if err != nil {
		t.Fatalf("DiscoverSkiaRequirementBashFree: %v", err)
	}
	want := "https://github.com/aseprite/skia/releases/download/m124-08a5439a6b/Skia-Windows-Release-x64.zip"
	if req.URL != want {
		t.Errorf("URL = %q, want %q", req.URL, want)
	}
	if req.Tag != "m124-08a5439a6b" || req.DirName != "skia-m124" || req.CPU != "x64" {
		t.Errorf("req = %+v", req)
	}
}

func TestDiscoverSkiaRequirementBashFreeMacShape(t *testing.T) {
	// The parser handles the macOS shape too (used nowhere today, but
	// it must not silently diverge from the script's actual lines).
	src := fixtureSource(t)
	req, err := DiscoverSkiaRequirementBashFree(src, SkiaPlatform{Name: "macos", CPU: "arm64"})
	if err != nil {
		t.Fatalf("macos shape: %v", err)
	}
	want := "https://github.com/aseprite/skia/releases/download/m124-08a5439a6b/Skia-macOS-Release-arm64.zip"
	if req.URL != want {
		t.Errorf("URL = %q, want %q", req.URL, want)
	}
}

func TestDiscoverSkiaRequirementBashFreeMutatedScript(t *testing.T) {
	for name, mutate := range map[string]func(files map[string]string){
		"no URL template": func(files map[string]string) {
			files["laf/misc/skia-url.sh"] = "#!/bin/sh\necho https://example.invalid/$tag\n"
		},
		"no asset name": func(files map[string]string) {
			files["laf/misc/skia-url.sh"] = strings.Replace(
				files["laf/misc/skia-url.sh"],
				"skia_file=Skia-Windows-$skia_build-$cpu.zip",
				"skia_file=changed-$something.zip", 1)
		},
	} {
		src := fixtureSource(t, func(files map[string]string) { mutate(files) })
		_, err := DiscoverSkiaRequirementBashFree(src, SkiaPlatform{Name: "windows", CPU: "x64"})
		if err == nil {
			t.Errorf("%s must fail safely", name)
		}
	}
}

func TestDiscoverSkiaRequirementBashFreeUnknownPlatform(t *testing.T) {
	src := fixtureSource(t)
	if _, err := DiscoverSkiaRequirementBashFree(src, SkiaPlatform{Name: "plan9", CPU: "x64"}); err == nil {
		t.Error("unknown platform must fail")
	}
}

func TestUnknownContractErrorMessage(t *testing.T) {
	e := UnknownContractError{Reasons: []string{"a", "b"}}
	msg := e.Error()
	if !strings.Contains(msg, "a") || !strings.Contains(msg, "b") {
		t.Errorf("message = %q", msg)
	}
}

func errorsAs(err error, target *UnknownContractError) bool {
	uc, ok := err.(UnknownContractError)
	if ok {
		*target = uc
	}
	return ok
}
