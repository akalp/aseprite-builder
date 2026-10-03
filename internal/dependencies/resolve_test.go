package dependencies

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/version"
)

func minCMake(v string) version.Version {
	vv, _ := version.Parse(v)
	return vv
}

func fakeVersionRunner(t *testing.T, versions map[string]string) *runner.FakeRunner {
	t.Helper()
	return &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		base := filepath.Base(c.Name)
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			// System probe (bare name) or absolute path probe.
			if v, ok := versions[base]; ok {
				switch base {
				case "cmake":
					return runner.Result{Stdout: "cmake version " + v + "\n"}, true
				case "ninja":
					return runner.Result{Stdout: v + "\n"}, true
				}
			}
		}
		return runner.Result{}, false
	}}
}

func testLayout(t *testing.T) paths.Layout {
	t.Helper()
	layout, err := paths.New(t.TempDir())
	if err != nil {
		t.Fatalf("paths.New: %v", err)
	}
	return layout
}

func TestDetectSystemToolCMake(t *testing.T) {
	r := fakeVersionRunner(t, map[string]string{"cmake": "3.31.6"})
	tool, ok, err := DetectSystemTool(context.Background(), r, "cmake")
	if err != nil || !ok {
		t.Fatalf("DetectSystemTool = %v %v, want ok", ok, err)
	}
	if tool.Version.String() != "3.31.6" || tool.Source != "system" || tool.BinDir != "" {
		t.Errorf("tool = %+v", tool)
	}
}

func TestDetectSystemToolMissing(t *testing.T) {
	// FakeRunner with no matcher and empty default simulates exec failure
	// only partially — model not-found by returning an error for the
	// bare-name probe.
	r := &runner.FakeRunner{Errs: map[string]error{
		"cmake --version": errors.New("exec: cmake: executable file not found in $PATH"),
	}}
	_, ok, err := DetectSystemTool(context.Background(), r, "cmake")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("tool should not be found")
	}
}

func TestDetectSystemToolUnparseable(t *testing.T) {
	r := &runner.FakeRunner{Results: map[string]runner.Result{
		"cmake --version": {Stdout: "give me your password plz"},
	}}
	_, ok, err := DetectSystemTool(context.Background(), r, "cmake")
	if err == nil || ok {
		t.Errorf("unparseable output must fail, got ok=%v err=%v", ok, err)
	}
}

func TestParseCMakeVersion(t *testing.T) {
	v, err := parseCMakeVersion("cmake version 4.1.2\n\nCMake suite maintained and supported by Kitware")
	if err != nil || v.String() != "4.1.2" {
		t.Errorf("v = %q err = %v", v, err)
	}
	if _, err := parseCMakeVersion("nonsense"); err == nil {
		t.Error("unparseable cmake output must fail")
	}
}

func TestParseNinjaVersion(t *testing.T) {
	v, err := parseNinjaVersion("1.13.1\n")
	if err != nil || v.String() != "1.13.1" {
		t.Errorf("v = %q err = %v", v, err)
	}
	if _, err := parseNinjaVersion("abc"); err == nil {
		t.Error("unparseable ninja output must fail")
	}
}

func TestResolvePrefersSystemTool(t *testing.T) {
	r := fakeVersionRunner(t, map[string]string{"cmake": "3.31.6", "ninja": "1.13.1"})
	layout := testLayout(t)
	fetchCalled := 0
	res := &Resolver{
		Runner: r,
		Layout: layout,
		Fetchers: map[string]Fetcher{"cmake": fetchFunc(func(ctx context.Context) (Tool, error) {
			fetchCalled++
			return Tool{}, errors.New("should not be fetched")
		})},
		MinVersions: map[string]version.Version{"cmake": minCMake("3.20")},
	}
	tool, err := res.Resolve(context.Background(), "cmake")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tool.Source != "system" {
		t.Errorf("Source = %q, want system", tool.Source)
	}
	if fetchCalled != 0 {
		t.Error("fetcher must not run when a compatible system tool exists")
	}
}

func TestResolveRejectsOldSystemToolAndDownloads(t *testing.T) {
	r := fakeVersionRunner(t, map[string]string{"cmake": "3.19.2"})
	// The downloaded distribution's binary probes as 4.1.2 (fresh and
	// functional), while the system cmake is too old.
	r.Match = func(c runner.Command) (runner.Result, bool) {
		if filepath.Base(c.Name) == "cmake" &&
			len(c.Args) == 1 && c.Args[0] == "--version" &&
			strings.Contains(c.Name, string(os.PathSeparator)+"4.1.2") {
			return runner.Result{Stdout: "cmake version 4.1.2\n"}, true
		}
		return runner.Result{Stdout: "cmake version 3.19.2\n"}, true
	}
	layout := testLayout(t)
	cachedBin := filepath.Join(layout.Tools, "cmake", "official", "4.1.2", "CMake.app", "Contents", "bin", "cmake")
	if err := os.MkdirAll(filepath.Dir(cachedBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedBin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := &Resolver{
		Runner: r,
		Layout: layout,
		Fetchers: map[string]Fetcher{"cmake": fetchFunc(func(ctx context.Context) (Tool, error) {
			return Tool{
				Name: "cmake", Version: minCMake("4.1.2"),
				Path: cachedBin, BinDir: filepath.Dir(cachedBin), Source: "cache",
			}, nil
		})},
		MinVersions: map[string]version.Version{"cmake": minCMake("3.20")},
	}
	tool, err := res.Resolve(context.Background(), "cmake")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tool.Source != "cache" {
		t.Errorf("Source = %q, want cache", tool.Source)
	}
	if tool.Version.String() != "4.1.2" {
		t.Errorf("Version = %q", tool.Version)
	}
}

func TestResolveSkipsBrokenCacheEntries(t *testing.T) {
	// System cmake is present but TOO OLD (so it must be skipped); the
	// cached distribution is present but its probe fails (broken).
	layout := testLayout(t)
	cachedBin := filepath.Join(layout.Tools, "cmake", "official", "4.1.2", "CMake.app", "Contents", "bin", "cmake")
	if err := os.MkdirAll(filepath.Dir(cachedBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedBin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &runner.FakeRunner{Errs: map[string]error{
		cachedBin + " --version": errors.New("exec format error"), // broken cached dist
	}, Match: func(c runner.Command) (runner.Result, bool) {
		if filepath.Base(c.Name) == "cmake" && len(c.Args) == 1 && c.Args[0] == "--version" {
			return runner.Result{Stdout: "cmake version 3.19.2\n"}, true
		}
		return runner.Result{}, false
	}}

	res := &Resolver{
		Runner: r,
		Layout: layout,
		Fetchers: map[string]Fetcher{"cmake": fetchFunc(func(ctx context.Context) (Tool, error) {
			return Tool{
				Name: "cmake", Version: minCMake("4.1.2"),
				Path: cachedBin, BinDir: filepath.Dir(cachedBin), Source: "cache",
			}, nil
		})},
		MinVersions: map[string]version.Version{"cmake": minCMake("3.20")},
	}
	// The fetcher returns 4.1.2 again; because its probe errors,
	// resolution must fail with a clear reason (and not panic).
	_, err := res.Resolve(context.Background(), "cmake")
	if err == nil {
		t.Fatal("resolution must fail when the only candidate keeps failing the probe")
	}
	if !strings.Contains(err.Error(), "does not run") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveNoToolAnywhere(t *testing.T) {
	// Probe reports exec error for both bare-name and abs-path probes.
	r := &runner.FakeRunner{Errs: map[string]error{
		"cmake --version": errors.New("not found"),
	}}
	layout := testLayout(t)
	res := &Resolver{
		Runner:      r,
		Layout:      layout,
		Fetchers:    map[string]Fetcher{},
		MinVersions: map[string]version.Version{"cmake": minCMake("3.20")},
	}
	_, err := res.Resolve(context.Background(), "cmake")
	if err == nil || !strings.Contains(err.Error(), "no compatible cmake") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveDownloadedTooOld(t *testing.T) {
	r := &runner.FakeRunner{Errs: map[string]error{
		"cmake --version": errors.New("not found"),
	}}
	layout := testLayout(t)
	res := &Resolver{
		Runner: r,
		Layout: layout,
		Fetchers: map[string]Fetcher{"cmake": fetchFunc(func(ctx context.Context) (Tool, error) {
			return Tool{Name: "cmake", Version: minCMake("3.19.0")}, nil
		})},
		MinVersions: map[string]version.Version{"cmake": minCMake("3.20")},
	}
	_, err := res.Resolve(context.Background(), "cmake")
	if err == nil || !strings.Contains(err.Error(), "older than the required minimum") {
		t.Errorf("err = %v", err)
	}
}

func TestCachedToolsNewestFirstAndSanitized(t *testing.T) {
	layout := testLayout(t)
	// Create two cached versions plus an unparsable directory to skip.
	for _, v := range []string{"4.1.2", "3.31.6"} {
		dir := filepath.Join(layout.Tools, "cmake", "official", v, "CMake.app", "Contents", "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmake"), []byte("stub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(layout.Tools, "cmake", "official", "garbage-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := &Resolver{Runner: &runner.NopRunner{}, Layout: layout}
	tools := res.cachedTools("cmake")
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want 2", tools)
	}
	if tools[0].Version.String() != "4.1.2" || tools[1].Version.String() != "3.31.6" {
		t.Errorf("order = %s, %s", tools[0].Version, tools[1].Version)
	}
	if tools[0].Source != "cache" {
		t.Errorf("Source = %q", tools[0].Source)
	}
}

func TestCheckReportsStatuses(t *testing.T) {
	// cmake present and new enough; ninja absent entirely (probe fails
	// like a real exec-not-found), with a fetcher configured.
	r := fakeVersionRunner(t, map[string]string{"cmake": "3.31.6"})
	r.Errs = map[string]error{"ninja --version": errors.New("exec: ninja: executable file not found in $PATH")}
	layout := testLayout(t)
	res := &Resolver{
		Runner:      r,
		Layout:      layout,
		Fetchers:    map[string]Fetcher{"ninja": NinjaFetcher{Layout: layout}},
		MinVersions: map[string]version.Version{"cmake": minCMake("3.20"), "ninja": minCMake("1.0")},
	}
	sysCheck, err := res.Check(context.Background(), "cmake")
	if err != nil {
		t.Fatal(err)
	}
	if sysCheck.Status != "system" {
		t.Errorf("cmake status = %q, want system", sysCheck.Status)
	}
	ninjaCheck, err := res.Check(context.Background(), "ninja")
	if err != nil {
		t.Fatal(err)
	}
	if ninjaCheck.Status != "download" {
		t.Errorf("ninja status = %q, want download (no fetch attempt in Check)", ninjaCheck.Status)
	}
}

// fetchFunc adapts a function into a Fetcher.
type fetchFunc func(context.Context) (Tool, error)

func (f fetchFunc) Fetch(ctx context.Context) (Tool, error) { return f(ctx) }
