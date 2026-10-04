package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/state"
)

// materializeInstalledApp creates the bundle layout the fallback
// verification expects, and wires its --version probe.
func materializeInstalledApp(t *testing.T, fk *runner.FakeRunner, version string) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Applications", "Aseprite.app")
	exe := filepath.Join(app, "Contents", "MacOS", "aseprite")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("macho"), 0o755); err != nil {
		t.Fatal(err)
	}
	fk.Match = func(c runner.Command) (runner.Result, bool) {
		if len(c.Args) == 1 && c.Args[0] == "--version" && strings.HasSuffix(c.Name, "aseprite") {
			return runner.Result{Stdout: "Aseprite v" + version + "\n"}, true
		}
		return runner.Result{}, false
	}
	return app
}

func TestRepairHealthyInstallationRunsTargetedFixes(t *testing.T) {
	svc, plat, fk, _ := newTestService(t)
	app := materializeInstalledApp(t, fk, "1.3.18.6")
	plat.installed = "1.3.18.6"
	plat.repairActions = []string{"recreated the CLI symlink /x"}
	if err := svc.opts.Store.SaveInstall(state.InstallState{
		AsepriteVersion: "1.3.18.6",
		AppPath:         app,
		Channel:         "stable",
		WorkspacePath:   "/tmp/ws",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Repair(context.Background(), nil)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if res.Version != "1.3.18.6" || res.AppPath != app {
		t.Errorf("result = %+v", res)
	}
	if !plat.repaired {
		t.Fatal("targeted repairs must run on a healthy installation")
	}
	if plat.repairReq.AppPath != app || plat.repairReq.SourceDir != "/tmp/ws" {
		t.Errorf("repair request = %+v, want the installed facts", plat.repairReq)
	}
	if plat.installCalls != 0 {
		t.Errorf("a healthy installation must not be rebuilt (installCalls = %d)", plat.installCalls)
	}
}

func TestRepairClearsMissingPreviousRecord(t *testing.T) {
	svc, plat, fk, _ := newTestService(t)
	app := materializeInstalledApp(t, fk, "1.3.18.6")
	plat.installed = "1.3.18.6"
	if err := svc.opts.Store.SaveInstall(state.InstallState{
		AsepriteVersion: "1.3.18.6",
		AppPath:         app,
		Channel:         "stable",
		PreviousAppPath: filepath.Join(t.TempDir(), "gone.app"),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Repair(context.Background(), nil); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	st, ok, err := svc.CurrentInstall()
	if err != nil || !ok {
		t.Fatalf("CurrentInstall: ok=%v err=%v", ok, err)
	}
	if st.PreviousAppPath != "" {
		t.Errorf("PreviousAppPath = %q, want the stale record cleared", st.PreviousAppPath)
	}
}

func TestRepairRebuildsOnMarkerMismatch(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	plat.installed = "1.3.18.5" // ownership marker disagrees with the state

	app := filepath.Join(t.TempDir(), "Applications", "Aseprite.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svc.opts.Store.SaveInstall(state.InstallState{
		AsepriteVersion: "1.3.18.6",
		AppPath:         app,
		Channel:         "stable",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Repair(context.Background(), nil)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if res.Version != "1.3.18.6" {
		t.Errorf("rebuilt version = %q", res.Version)
	}
	if plat.installCalls == 0 {
		t.Error("a marker mismatch must trigger a rebuild")
	}
	if plat.repaired {
		t.Error("targeted repairs only run on the healthy path")
	}
}

func TestRepairRebuildsOnMissingMarker(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	// plat.installed stays "" — the ownership marker is missing.

	app := filepath.Join(t.TempDir(), "Applications", "Aseprite.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svc.opts.Store.SaveInstall(state.InstallState{
		AsepriteVersion: "1.3.18.6",
		AppPath:         app,
		Channel:         "stable",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Repair(context.Background(), nil); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if plat.installCalls == 0 {
		t.Error("a missing marker must trigger a rebuild")
	}
}

func TestRepairRebuildsWhenBundleMissing(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	plat.installed = "1.3.18.6"

	if err := svc.opts.Store.SaveInstall(state.InstallState{
		AsepriteVersion: "1.3.18.6",
		AppPath:         filepath.Join(t.TempDir(), "missing", "Aseprite.app"),
		Channel:         "stable",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Repair(context.Background(), nil); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if plat.installCalls == 0 {
		t.Error("a missing bundle must trigger a rebuild")
	}
}

func TestDoctorInspectsCachedSourceContract(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	materializeSourceFixture(t, filepath.Join(sourcesDir, "aseprite-1.3.18.6"))

	report, err := svc.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Contract == nil {
		t.Fatal("a cached source tree must produce a build-contract row")
	}
	if !report.Contract.Recognized {
		t.Fatalf("contract = %+v, want recognized", report.Contract)
	}
	if !strings.Contains(report.Contract.Fingerprint, "cmake-min=3.20") {
		t.Errorf("fingerprint = %q", report.Contract.Fingerprint)
	}
	var found bool
	for _, row := range report.DoctorRows() {
		if row.Name == "Build contract" {
			found = true
			if !row.OK {
				t.Errorf("build-contract row = %+v, want OK", row)
			}
		}
	}
	if !found {
		t.Error("doctor rows must include the build contract")
	}
}

func TestDoctorReportsUnrecognizedCachedSource(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	wireBuildFake(t, fk, sourcesDir, "1.3.18.6")
	// A source dir without the contract markers.
	broken := filepath.Join(sourcesDir, "aseprite-9.9.9")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "CMakeLists.txt"), []byte("not a contract"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := svc.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Contract == nil || report.Contract.Recognized {
		t.Fatalf("contract = %+v, want unrecognized", report.Contract)
	}
	if report.Status != "issues" {
		t.Errorf("status = %q, want issues for an unrecognized cached source", report.Status)
	}
}

func TestDoctorReportsIntegrationHealth(t *testing.T) {
	svc, plat, fk, _ := newTestService(t)
	_ = fk
	ok := false
	plat.integrationOK = &ok
	plat.integrationDetail = "desktop entry missing — run repair"
	if err := svc.opts.Store.SaveInstall(state.InstallState{
		AsepriteVersion: "1.3.18.6",
		AppPath:         filepath.Join(t.TempDir(), "missing", "Aseprite.app"),
		Channel:         "stable",
	}); err != nil {
		t.Fatal(err)
	}

	report, err := svc.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Integration == nil || report.Integration.OK {
		t.Fatalf("integration = %+v, want an unhealthy report", report.Integration)
	}
	if report.Status != "issues" {
		t.Errorf("status = %q, want issues", report.Status)
	}
	var found bool
	for _, row := range report.DoctorRows() {
		if row.Name == "CLI integration" {
			found = true
			if row.OK {
				t.Errorf("integration row = %+v, want not OK", row)
			}
		}
	}
	if !found {
		t.Error("doctor rows must include CLI integration")
	}
}
