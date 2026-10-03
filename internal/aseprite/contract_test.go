package aseprite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aseprite-builder/aseprite-builder/internal/runner"
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
		"laf/misc/skia-url.sh":    "# skia url resolver",
		"src/main/osx/Info.plist": "<plist>",
		"src/CMakeLists.txt":      "# output contract\n# ${CMAKE_BINARY_DIR}/bin/Aseprite.app/Contents/Resources/data\n",
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
	c, err := NewInspector().Inspect(src, true)
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
	if c.Level != 1 {
		t.Errorf("Level = %d, want 1", c.Level)
	}
	if !strings.Contains(c.ContractFingerprint(), "cmake-min=3.20") {
		t.Errorf("fingerprint = %q", c.ContractFingerprint())
	}
}

func TestInspectMissingSkiaTag(t *testing.T) {
	src := fixtureSource(t, func(files map[string]string) {
		delete(files, "laf/misc/skia-tag.txt")
	})
	_, err := NewInspector().Inspect(src, true)
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
	_, err := NewInspector().Inspect(src, true)
	if err == nil || !strings.Contains(err.Error(), "laf/CMakeLists.txt") {
		t.Errorf("err = %v, want missing laf/CMakeLists.txt", err)
	}
}

func TestInspectNonDarwinIgnoresPlist(t *testing.T) {
	// wantsAppBundle=false models platforms without a bundle output
	// contract (Linux/Windows today).
	src := fixtureSource(t, func(files map[string]string) {
		delete(files, "src/main/osx/Info.plist")
	})
	c, err := NewInspector().Inspect(src, false)
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
	_, err := NewInspector().Inspect(src, true)
	if err == nil || !strings.Contains(err.Error(), "CMake version") {
		t.Errorf("err = %v", err)
	}
}

func TestInspectMissingProject(t *testing.T) {
	src := fixtureSource(t, func(files map[string]string) {
		files["CMakeLists.txt"] = "cmake_minimum_required(VERSION 3.20)\n"
	})
	_, err := NewInspector().Inspect(src, true)
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
	if got := strings.Join(calls[0].Args, " "); got != src+"/laf/misc/skia-url.sh Release macos arm64" {
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
