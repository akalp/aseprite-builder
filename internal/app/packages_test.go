package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/state"
)

// packagePlatform extends fakePlatform with the system-package
// provider/installer capabilities (Linux's optional interfaces).
type packagePlatform struct {
	fakePlatform
	missing []packages.Package
	command string
	calls   [][]packages.Package
	err     error
}

func (f *packagePlatform) SystemPackages(context.Context) (packages.Report, error) {
	return packages.Report{Manager: "apt", Missing: f.missing, Command: f.command}, nil
}

func (f *packagePlatform) InstallSystemPackages(_ context.Context, pkgs []packages.Package) error {
	f.calls = append(f.calls, pkgs)
	return f.err
}

func newPackageService(t *testing.T, plat platform.Platform) *Service {
	t.Helper()
	layout, err := paths.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	svc, err := New(Options{
		BuilderVersion: "test",
		Layout:         layout,
		Platform:       plat,
		Releases:       fakeReleases{},
		Runner:         &runner.FakeRunner{},
		Store:          state.Store{Dir: layout.State},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestSystemPackagesEmptyWithoutBackend(t *testing.T) {
	svc := newPackageService(t, &fakePlatform{})
	report, err := svc.SystemPackages(context.Background())
	if err != nil {
		t.Fatalf("SystemPackages: %v", err)
	}
	if report.Manager != "" || len(report.Missing) != 0 || report.Command != "" {
		t.Errorf("report = %+v, want empty", report)
	}
}

func TestSystemPackagesReportsMissingPackages(t *testing.T) {
	plat := &packagePlatform{
		missing: []packages.Package{{Name: "ninja-build", What: "build backend"}},
		command: "sudo apt-get install ninja-build",
	}
	svc := newPackageService(t, plat)
	report, err := svc.SystemPackages(context.Background())
	if err != nil {
		t.Fatalf("SystemPackages: %v", err)
	}
	if report.Manager != "apt" || len(report.Missing) != 1 || report.Command == "" {
		t.Errorf("report = %+v", report)
	}
}

func TestInstallSystemPackagesRequiresBackend(t *testing.T) {
	svc := newPackageService(t, &fakePlatform{})
	err := svc.InstallSystemPackages(context.Background(), []packages.Package{{Name: "g++"}})
	if err == nil || !strings.Contains(err.Error(), "no system package installer") {
		t.Fatalf("err = %v, want the no-backend error", err)
	}
}

func TestInstallSystemPackagesEmptyIsNoop(t *testing.T) {
	plat := &packagePlatform{}
	svc := newPackageService(t, plat)
	if err := svc.InstallSystemPackages(context.Background(), nil); err != nil {
		t.Fatalf("InstallSystemPackages(nil): %v", err)
	}
	if len(plat.calls) != 0 {
		t.Errorf("installer calls = %v, want none", plat.calls)
	}
}

func TestInstallSystemPackagesForwardsPackages(t *testing.T) {
	plat := &packagePlatform{}
	svc := newPackageService(t, plat)
	pkgs := []packages.Package{{Name: "g++"}, {Name: "cmake"}}
	if err := svc.InstallSystemPackages(context.Background(), pkgs); err != nil {
		t.Fatalf("InstallSystemPackages: %v", err)
	}
	if len(plat.calls) != 1 || len(plat.calls[0]) != 2 {
		t.Fatalf("installer calls = %v", plat.calls)
	}
}

func TestInstallSystemPackagesErrorIsWrapped(t *testing.T) {
	plat := &packagePlatform{err: errors.New("sudo failed")}
	svc := newPackageService(t, plat)
	err := svc.InstallSystemPackages(context.Background(), []packages.Package{{Name: "g++"}})
	if err == nil || !strings.Contains(err.Error(), "install system packages") {
		t.Fatalf("err = %v, want wrapping", err)
	}
}
