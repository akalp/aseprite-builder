package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/state"
)

func TestInstallCustomInstallDirReachesPlatform(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	custom := filepath.Join(t.TempDir(), "custom", "apps")

	result, err := svc.Install(context.Background(), InstallOptions{InstallDir: custom}, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := filepath.Dir(result.AppPath); got != custom {
		t.Errorf("install path root = %q, want %q", got, custom)
	}
	if plat.installRoot != custom {
		t.Errorf("platform install root = %q, want %q", plat.installRoot, custom)
	}
	st, ok, err := svc.CurrentInstall()
	if err != nil || !ok {
		t.Fatalf("CurrentInstall: ok=%v err=%v", ok, err)
	}
	if filepath.Dir(st.AppPath) != custom {
		t.Errorf("persisted install path = %q, want under %q", st.AppPath, custom)
	}
}

func TestInstallDirFromPersistedConfig(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	custom := filepath.Join(t.TempDir(), "from-config")

	cfg := state.DefaultConfig()
	cfg.InstallDir = custom
	if err := svc.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if _, err := svc.Install(context.Background(), InstallOptions{}, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if plat.installRoot != custom {
		t.Errorf("platform install root = %q, want the persisted %q", plat.installRoot, custom)
	}
}

func TestSaveConfigRejectsRelativeInstallDir(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	cfg := state.DefaultConfig()
	cfg.InstallDir = "relative/apps"
	err := svc.SaveConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v, want a relative-path rejection", err)
	}
}

func TestRetainBuildsDisabledReachesPlatform(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	retain := false

	if _, err := svc.Install(context.Background(), InstallOptions{RetainBuilds: &retain}, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !plat.disableRetention {
		t.Error("the retention preference must reach the platform")
	}
}

func TestRetainBuildsDefaultsToPlatformRetention(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")

	if _, err := svc.Install(context.Background(), InstallOptions{}, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if plat.disableRetention {
		t.Error("the default keeps the previous build (spec §24)")
	}
}

func TestKeepSourcesSkipsPruning(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")

	olderA := filepath.Join(sourcesDir, "aseprite-1.0.0")
	olderB := filepath.Join(sourcesDir, "aseprite-1.0.1")
	for _, dir := range []string{olderA, olderB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := svc.Install(context.Background(), InstallOptions{KeepSources: true}, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, dir := range []string{olderA, olderB} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("source retention must keep %s: %v", dir, err)
		}
	}

	// The default prunes: with maxRetainedGenerations=1 the newest extra
	// generation survives, the older one is removed.
	if _, err := svc.Install(context.Background(), InstallOptions{}, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(olderA); !os.IsNotExist(err) {
		t.Errorf("default pruning must remove the oldest generation, stat err = %v", err)
	}
	if _, err := os.Stat(olderB); err != nil {
		t.Errorf("the newest extra generation is retained: %v", err)
	}
}

func TestDoctorHonorsCachedToolPreference(t *testing.T) {
	svc, _, fk, _ := newTestService(t)

	// A cached cmake distribution plus a compatible system tool: the
	// configured preference decides which one doctor reports. The cache
	// layout follows the host (bin/cmake.exe on Windows).
	cachedRel := filepath.Join("CMake.app", "Contents", "bin", "cmake")
	if runtime.GOOS == "windows" {
		cachedRel = filepath.Join("bin", "cmake.exe")
	}
	cachedBin := filepath.Join(svc.opts.Layout.Tools, "cmake", "official", "4.1.2", cachedRel)
	if err := os.MkdirAll(filepath.Dir(cachedBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedBin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	fk.Match = func(c runner.Command) (runner.Result, bool) {
		base := strings.TrimSuffix(filepath.Base(c.Name), ".exe")
		if len(c.Args) == 1 && c.Args[0] == "--version" && base == "cmake" {
			if strings.Contains(c.Name, "4.1.2") {
				return runner.Result{Stdout: "cmake version 4.1.2\n"}, true
			}
			return runner.Result{Stdout: "cmake version 3.31.6\n"}, true
		}
		if len(c.Args) == 1 && c.Args[0] == "--version" && base == "ninja" {
			return runner.Result{Stdout: "1.13.1\n"}, true
		}
		return runner.Result{}, false
	}
	cfg := state.DefaultConfig()
	cfg.ToolPreference = "cached"
	if err := svc.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	report, err := svc.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var cmake string
	for _, tool := range report.Tools {
		if tool.Name == "cmake" {
			cmake = tool.Status
		}
	}
	if cmake != "cache" {
		t.Errorf("cmake status = %q, want cache under the cached preference", cmake)
	}
}
