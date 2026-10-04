package pacman

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

func fakeTools(t *testing.T) (pacmanPath string) {
	t.Helper()
	dir := t.TempDir()
	pacmanPath = filepath.Join(dir, "pacman")
	if err := os.WriteFile(pacmanPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return pacmanPath
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
	for _, want := range []string{"gcc", "clang", "cmake", "ninja", "libglvnd", "libx11"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("required package %q missing from the table", want)
		}
	}
}

func TestDetectRequiresPacman(t *testing.T) {
	pacmanPath := fakeTools(t)
	p := Platform{Runner: &runner.FakeRunner{}, Pacman: pacmanPath}
	if !p.Detect(context.Background()) {
		t.Error("pacman present must detect")
	}
	p.Pacman = filepath.Join(t.TempDir(), "missing")
	if p.Detect(context.Background()) {
		t.Error("missing pacman must not detect")
	}
}

func TestMissingPackagesQueriesLocalDatabase(t *testing.T) {
	f := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if c.Name != "/usr/bin/pacman" || len(c.Args) != 2 || c.Args[0] != "-Qi" {
			return runner.Result{}, false
		}
		if c.Args[1] == "gcc" {
			return runner.Result{ExitCode: 0}, true
		}
		return runner.Result{ExitCode: 1}, true
	}}
	p := Platform{Runner: f, Pacman: "/usr/bin/pacman"}
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
		"/usr/bin/pacman -Qi gcc": errors.New("exec: no such file"),
	}}
	p := Platform{Runner: f, Pacman: "/usr/bin/pacman"}
	if _, err := p.MissingPackages(context.Background(), []packages.Package{{Name: "gcc"}}); err == nil {
		t.Fatal("a start failure must surface as an error")
	}
}

func TestInstallCommand(t *testing.T) {
	pkgs := []packages.Package{{Name: "gcc"}, {Name: "cmake"}}
	if got, want := InstallCommand(pkgs), "sudo pacman -S --needed gcc cmake"; got != want {
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
	p := Platform{Runner: f, Pacman: "/usr/bin/pacman"}
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
