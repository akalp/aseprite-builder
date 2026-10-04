package xbps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/runner"
)

func fakeTools(t *testing.T) (installPath, queryPath string) {
	t.Helper()
	dir := t.TempDir()
	installPath = filepath.Join(dir, "xbps-install")
	queryPath = filepath.Join(dir, "xbps-query")
	for _, p := range []string{installPath, queryPath} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return installPath, queryPath
}

func TestRequiredPackages(t *testing.T) {
	byName := map[string]packages.Package{}
	for _, p := range RequiredPackages() {
		if p.Name == "" || p.What == "" {
			t.Errorf("package %+v must have a name and rationale", p)
		}
		if strings.ContainsAny(p.Name, ">=<~") {
			t.Errorf("package name %q looks like a version constraint", p.Name)
		}
		byName[p.Name] = p
	}
	for _, want := range []string{"gcc", "clang", "cmake", "ninja", "MesaLib-devel", "libX11-devel"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("required package %q missing from the table", want)
		}
	}
}

func TestDetectRequiresInstallAndQuery(t *testing.T) {
	installPath, queryPath := fakeTools(t)
	p := Platform{Runner: &runner.FakeRunner{}, XbpsInstall: installPath, XbpsQuery: queryPath}
	if !p.Detect(context.Background()) {
		t.Error("both tools present must detect")
	}
	p.XbpsQuery = filepath.Join(t.TempDir(), "missing")
	if p.Detect(context.Background()) {
		t.Error("missing xbps-query must not detect")
	}
	p.XbpsQuery = queryPath
	p.XbpsInstall = filepath.Join(t.TempDir(), "missing")
	if p.Detect(context.Background()) {
		t.Error("missing xbps-install must not detect")
	}
}

func TestMissingPackagesQueriesInstalledDatabase(t *testing.T) {
	f := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if c.Name != "/usr/bin/xbps-query" || len(c.Args) != 1 {
			return runner.Result{}, false
		}
		if c.Args[0] == "gcc" {
			return runner.Result{ExitCode: 0}, true
		}
		// xbps-query exits non-zero when the package is not installed.
		return runner.Result{ExitCode: 2}, true
	}}
	p := Platform{Runner: f, XbpsQuery: "/usr/bin/xbps-query"}
	wanted := []packages.Package{
		{Name: "gcc", What: "C++ compiler"},
		{Name: "cmake", What: "build system"},
	}
	missing, err := p.MissingPackages(context.Background(), wanted)
	if err != nil {
		t.Fatalf("MissingPackages: %v", err)
	}
	if len(missing) != 1 || missing[0].Name != "cmake" {
		t.Fatalf("missing = %+v, want only cmake", missing)
	}
}

func TestMissingPackagesSurfacesProbeFailure(t *testing.T) {
	f := &runner.FakeRunner{Errs: map[string]error{
		"/usr/bin/xbps-query gcc": errors.New("exec: no such file"),
	}}
	p := Platform{Runner: f, XbpsQuery: "/usr/bin/xbps-query"}
	if _, err := p.MissingPackages(context.Background(), []packages.Package{{Name: "gcc"}}); err == nil {
		t.Fatal("a start failure must surface as an error")
	}
}

func TestInstallCommand(t *testing.T) {
	pkgs := []packages.Package{{Name: "gcc"}, {Name: "cmake"}}
	if got, want := InstallCommand(pkgs), "sudo xbps-install -S gcc cmake"; got != want {
		t.Errorf("InstallCommand = %q, want %q", got, want)
	}
	if InstallCommand(nil) != "" {
		t.Error("empty package list must yield an empty command")
	}
	if got := (Platform{}).InstallCommand(pkgs); got != InstallCommand(pkgs) {
		t.Errorf("method command = %q, func command = %q", got, InstallCommand(pkgs))
	}
}

func TestInstallRunsVisibleInteractiveSudo(t *testing.T) {
	f := &runner.FakeRunner{}
	p := Platform{Runner: f, XbpsInstall: "/usr/bin/xbps-install", XbpsQuery: "/usr/bin/xbps-query"}
	pkgs := []packages.Package{{Name: "gcc"}, {Name: "cmake"}}
	if err := p.Install(context.Background(), pkgs); err != nil {
		t.Fatalf("Install: %v", err)
	}
	calls := f.Called()
	if len(calls) != 1 || calls[0].Name != "sudo" || !calls[0].Interactive {
		t.Fatalf("calls = %+v, want one interactive sudo", calls)
	}
	if "sudo "+strings.Join(calls[0].Args, " ") != InstallCommand(pkgs) {
		t.Errorf("executed command %q differs from instruction %q",
			"sudo "+strings.Join(calls[0].Args, " "), InstallCommand(pkgs))
	}
}

func TestInstallEmptyIsNoop(t *testing.T) {
	f := &runner.FakeRunner{}
	p := Platform{Runner: f}
	if err := p.Install(context.Background(), nil); err != nil {
		t.Fatalf("Install(nil): %v", err)
	}
	if len(f.Called()) != 0 {
		t.Errorf("no command must run for an empty package list: %v", f.Called())
	}
}
