package apt

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

func fakeTools(t *testing.T) (aptGet, dpkgQuery string) {
	t.Helper()
	dir := t.TempDir()
	aptGet = filepath.Join(dir, "apt-get")
	dpkgQuery = filepath.Join(dir, "dpkg-query")
	for _, p := range []string{aptGet, dpkgQuery} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return aptGet, dpkgQuery
}

func TestDetectRequiresTools(t *testing.T) {
	// Family applicability is the selector's job; apt.Detect is a pure
	// binary-presence check.
	aptGet, dpkgQuery := fakeTools(t)
	p := Platform{Runner: &runner.FakeRunner{}, APTGet: aptGet, DpkgQuery: dpkgQuery}
	if !p.Detect(context.Background()) {
		t.Error("apt tools present must detect")
	}

	p.DpkgQuery = filepath.Join(t.TempDir(), "missing")
	if p.Detect(context.Background()) {
		t.Error("missing dpkg-query must not detect")
	}

	p.DpkgQuery = dpkgQuery
	p.APTGet = filepath.Join(t.TempDir(), "missing")
	if p.Detect(context.Background()) {
		t.Error("missing apt-get must not detect")
	}
}

func TestRequiredPackagesCarryNamesAndRationale(t *testing.T) {
	pkgs := RequiredPackages()
	if len(pkgs) == 0 {
		t.Fatal("required packages are empty")
	}
	byName := map[string]packages.Package{}
	for _, p := range pkgs {
		if p.Name == "" || p.What == "" {
			t.Errorf("package %+v must have a name and rationale", p)
		}
		// Names never carry version constraints.
		if strings.ContainsAny(p.Name, ">=<~") {
			t.Errorf("package name %q looks like a version constraint", p.Name)
		}
		byName[p.Name] = p
	}
	for _, want := range []string{"g++", "cmake", "ninja-build", "libx11-dev", "libfontconfig1-dev"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("required package %q missing from the table", want)
		}
	}
}

func TestMissingPackagesReadsDpkgStatus(t *testing.T) {
	// dpkg-query prints a line per known package; unknown packages are
	// absent from stdout and exit non-zero. Both mean "missing".
	f := &runner.FakeRunner{
		Results: map[string]runner.Result{
			"/usr/bin/dpkg-query -W -f=${binary:Package}\t${db:Status-Status}\n g++ cmake ninja-build libx11-dev": {
				Stdout: "g++\tinstalled\ncmake\tinstalled\nlibx11-dev\tnot-installed\n",
				Stderr: "dpkg-query: no packages found matching ninja-build\n",
			},
		},
		Default: runner.Result{ExitCode: 1},
	}
	p := Platform{
		Runner:    f,
		APTGet:    "/usr/bin/apt-get",
		DpkgQuery: "/usr/bin/dpkg-query",
	}
	wanted := []packages.Package{
		{Name: "g++", What: "C++ compiler"},
		{Name: "cmake", What: "build system"},
		{Name: "ninja-build", What: "build backend"},
		{Name: "libx11-dev", What: "X11 (dev)"},
	}
	missing, err := p.MissingPackages(context.Background(), wanted)
	if err != nil {
		t.Fatalf("MissingPackages: %v", err)
	}
	var names []string
	for _, m := range missing {
		names = append(names, m.Name)
	}
	want := []string{"ninja-build", "libx11-dev"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("missing = %v, want %v", names, want)
	}
}

func TestMissingPackagesStartFailureIsFatal(t *testing.T) {
	f := &runner.FakeRunner{Errs: map[string]error{
		"/usr/bin/dpkg-query -W -f=${binary:Package}\t${db:Status-Status}\n g++": errors.New("exec: not found"),
	}}
	p := Platform{Runner: f, DpkgQuery: "/usr/bin/dpkg-query"}
	if _, err := p.MissingPackages(context.Background(), []packages.Package{{Name: "g++"}}); err == nil {
		t.Fatal("a start failure must surface as an error")
	}
}

func TestInstallCommand(t *testing.T) {
	pkgs := []packages.Package{{Name: "g++"}, {Name: "cmake"}, {Name: "ninja-build"}}
	got := InstallCommand(pkgs)
	want := "sudo apt-get install g++ cmake ninja-build"
	if got != want {
		t.Errorf("InstallCommand = %q, want %q", got, want)
	}
	if InstallCommand(nil) != "" {
		t.Error("empty package list must yield an empty command")
	}
}

func TestInstallRunsVisibleInteractiveSudo(t *testing.T) {
	f := &runner.FakeRunner{}
	p := Platform{Runner: f, APTGet: "/usr/bin/apt-get", DpkgQuery: "/usr/bin/dpkg-query"}
	pkgs := []packages.Package{{Name: "g++"}, {Name: "cmake"}}
	if err := p.Install(context.Background(), pkgs); err != nil {
		t.Fatalf("Install: %v", err)
	}
	calls := f.Called()
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want exactly one", calls)
	}
	got := calls[0]
	if got.Name != "sudo" {
		t.Errorf("command name = %q, want sudo", got.Name)
	}
	if strings.Join(got.Args, " ") != "apt-get install g++ cmake" {
		t.Errorf("args = %v", got.Args)
	}
	if !got.Interactive {
		t.Error("package installation must run with the terminal attached (Interactive)")
	}
	// The executed command must equal the instruction shown to the user.
	if "sudo "+strings.Join(got.Args, " ") != InstallCommand(pkgs) {
		t.Errorf("executed command %q differs from instruction %q",
			"sudo "+strings.Join(got.Args, " "), InstallCommand(pkgs))
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

func TestInstallExitCodeSurfaces(t *testing.T) {
	f := &runner.FakeRunner{Default: runner.Result{ExitCode: 100}}
	p := Platform{Runner: f}
	err := p.Install(context.Background(), []packages.Package{{Name: "cmake"}})
	if err == nil || !strings.Contains(err.Error(), "code 100") {
		t.Errorf("err = %v, want exit-code context", err)
	}
}

func TestManagerMethodsMatchPackageFunctions(t *testing.T) {
	p := Platform{}
	if got := p.RequiredPackages(); len(got) != len(RequiredPackages()) {
		t.Errorf("method table = %d entries, func table = %d", len(got), len(RequiredPackages()))
	}
	pkgs := []packages.Package{{Name: "cmake"}}
	if got := p.InstallCommand(pkgs); got != InstallCommand(pkgs) {
		t.Errorf("method command = %q, func command = %q", got, InstallCommand(pkgs))
	}
}
