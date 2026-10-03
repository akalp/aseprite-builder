package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/dependencies"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/state"
)

func TestConfigDefaultsWhenAbsent(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	cfg, err := svc.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg != state.DefaultConfig() {
		t.Errorf("cfg = %+v, want %+v", cfg, state.DefaultConfig())
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	cfg := state.DefaultConfig()
	cfg.KeepWorkspace = true
	cfg.Verbose = true
	cfg.NoticeAcknowledged = true
	if err := svc.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := svc.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got != cfg {
		t.Errorf("round trip = %+v, want %+v", got, cfg)
	}
}

func TestSaveConfigNormalizesChannel(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	cfg := state.DefaultConfig()
	cfg.Channel = "beta" // only stable is supported
	if err := svc.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := svc.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got.Channel != "stable" {
		t.Errorf("persisted channel = %q, want normalized stable", got.Channel)
	}
}

// TestInstallHonorsPersistedKeepWorkspace: the settings screen writes
// config.json; the install pipeline must actually honor it even when
// the per-invocation request says nothing (today only the CLI's
// --keep-workspace flag set this).
func TestInstallHonorsPersistedKeepWorkspace(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")

	cfg := state.DefaultConfig()
	cfg.KeepWorkspace = true
	if err := svc.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	result, err := svc.Install(context.Background(), InstallOptions{}, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.Workspace, "build")); err != nil {
		t.Errorf("build workspace should be retained by config.json: %v", err)
	}
}

func TestVerboseConfigRecordsProgressEventsInLog(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")

	cfg := state.DefaultConfig()
	cfg.Verbose = true
	if err := svc.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	result, err := svc.Install(context.Background(), InstallOptions{}, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	data, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "progress: stage=\"Compile Aseprite\"") {
		t.Errorf("verbose log missing progress lines:\n%s", data)
	}
}

func TestVerboseOffByDefault(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	result, err := svc.Install(context.Background(), InstallOptions{}, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	data, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "progress: stage=") {
		t.Errorf("non-verbose log must not contain progress lines:\n%s", data)
	}
}

func TestDoctorRowsAreTheSharedFacts(t *testing.T) {
	report := DoctorReport{
		System: platform.SystemInfo{OS: "darwin", OSVersion: "15.2", Arch: "arm64", Supported: true},
		Requirements: []platform.Check{
			{Name: "Xcode", OK: false, Fix: "xcode-select --install"},
		},
		Tools: []dependencies.Check{{Name: "CMake", Status: "ok", Detail: "system"}},
		Skia:  SkiaDoctor{Status: "none-cached"},
	}
	rows := report.DoctorRows()

	if len(rows) < 4 {
		t.Fatalf("rows = %+v", rows)
	}
	// A failing requirement carries the fix hint as the detail.
	if rows[2].OK || rows[2].Name != "Xcode" || rows[2].Detail != "xcode-select --install" {
		t.Errorf("requirement row = %+v", rows[2])
	}
	// Installation summary row exists with the standard name.
	last := rows[len(rows)-1]
	if last.Name != "Installation" {
		t.Errorf("last row = %+v", last)
	}
}

func TestDoctorRowsInstallationNone(t *testing.T) {
	report := DoctorReport{
		System: platform.SystemInfo{OS: "darwin", OSVersion: "15.2", Arch: "arm64", Supported: true},
	}
	for _, r := range report.DoctorRows() {
		if r.Name == "Installation" {
			if !r.OK || r.Detail != "none (nothing installed yet)" {
				t.Errorf("installation row = %+v", r)
			}
		}
	}
}

func TestPurgeCachesOnlyTouchesLayoutDirs(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	layout := svc.Layout()

	for _, dir := range []string{layout.Sources, layout.Tools, layout.Skia, layout.Logs, layout.Downloads} {
		if err := os.MkdirAll(filepath.Join(dir, "keepme"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	results := svc.PurgeCaches(PurgeOptions{Sources: true, Tools: true, Skia: true, Logs: true})
	if len(results) != 4 {
		t.Fatalf("results = %+v", results)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("purge %s: %v", r.What, r.Err)
		}
	}
	// Purged dirs are gone...
	for _, dir := range []string{layout.Sources, layout.Tools, layout.Skia, layout.Logs} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("dir %s still exists", dir)
		}
	}
	// ...and unselected dirs survive.
	if _, err := os.Stat(layout.Downloads); err != nil {
		t.Errorf("unselected dir was removed: %v", err)
	}
}

func TestPurgeCachesEmptySelection(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if r := svc.PurgeCaches(PurgeOptions{}); len(r) != 0 {
		t.Errorf("results = %+v, want none", r)
	}
}
