package packages

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/runner"
)

func TestParseOSReleaseUbuntu(t *testing.T) {
	rel := ParseOSRelease(`
PRETTY_NAME="Ubuntu 24.04.1 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
VERSION="24.04.1 LTS (Noble Numbat)"
VERSION_CODENAME=noble
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
# a comment
`)
	if rel.ID != "ubuntu" {
		t.Errorf("ID = %q", rel.ID)
	}
	if !reflect.DeepEqual(rel.IDLike, []string{"debian"}) {
		t.Errorf("IDLike = %v", rel.IDLike)
	}
	if rel.VersionID != "24.04" || rel.VersionCodename != "noble" {
		t.Errorf("version fields = %q / %q", rel.VersionID, rel.VersionCodename)
	}
	if rel.PrettyName != "Ubuntu 24.04.1 LTS" {
		t.Errorf("PrettyName = %q", rel.PrettyName)
	}
	if !rel.IsDebianLike() {
		t.Error("ubuntu must be debian-like")
	}
}

func TestParseOSReleaseDebianDerivative(t *testing.T) {
	rel := ParseOSRelease("ID=linuxmint\nID_LIKE=\"ubuntu debian\"\nVERSION_ID=21.3\n")
	if !rel.IsDebianLike() {
		t.Error("linux mint (ID_LIKE=ubuntu debian) must be debian-like")
	}
}

func TestParseOSReleaseQuotingAndGarbage(t *testing.T) {
	rel := ParseOSRelease(`
ID='fedora'
ID_LIKE="rhel centos"
BROKEN LINE WITHOUT EQUALS
EMPTY=
VERSION_ID="39"
`)
	if rel.ID != "fedora" || !reflect.DeepEqual(rel.IDLike, []string{"rhel", "centos"}) {
		t.Errorf("rel = %+v", rel)
	}
	if rel.VersionID != "39" {
		t.Errorf("VersionID = %q", rel.VersionID)
	}
	if rel.IsDebianLike() {
		t.Error("fedora must not be debian-like")
	}
}

func TestParseOSReleaseEmpty(t *testing.T) {
	rel := ParseOSRelease("")
	if rel.IsDebianLike() {
		t.Error("empty release must not claim a family")
	}
	if rel.Display() != "unknown" {
		t.Errorf("Display() = %q", rel.Display())
	}
}

func TestOSReleaseDisplayFallbacks(t *testing.T) {
	cases := []struct {
		rel  OSRelease
		want string
	}{
		{OSRelease{PrettyName: "Debian GNU/Linux 12 (bookworm)"}, "Debian GNU/Linux 12 (bookworm)"},
		{OSRelease{ID: "ubuntu", VersionID: "24.04"}, "ubuntu 24.04"},
		{OSRelease{ID: "arch"}, "arch"},
		{OSRelease{}, "unknown"},
	}
	for _, c := range cases {
		if got := c.rel.Display(); got != c.want {
			t.Errorf("Display(%+v) = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestFamilyPredicates(t *testing.T) {
	type match struct {
		debian, fedora, arch, suse, void_ bool
	}
	cases := []struct {
		name string
		rel  OSRelease
		want match
	}{
		{"ubuntu", OSRelease{ID: "ubuntu", IDLike: []string{"debian"}}, match{debian: true}},
		{"debian", OSRelease{ID: "debian"}, match{debian: true}},
		{"linuxmint", OSRelease{ID: "linuxmint", IDLike: []string{"ubuntu"}}, match{debian: true}},
		{"fedora", OSRelease{ID: "fedora"}, match{fedora: true}},
		{"rhel", OSRelease{ID: "rhel", IDLike: []string{"fedora"}}, match{fedora: true}},
		{"rocky", OSRelease{ID: "rocky", IDLike: []string{"rhel", "centos", "fedora"}}, match{fedora: true}},
		{"almalinux", OSRelease{ID: "almalinux", IDLike: []string{"rhel", "centos", "fedora"}}, match{fedora: true}},
		{"arch", OSRelease{ID: "arch"}, match{arch: true}},
		{"manjaro", OSRelease{ID: "manjaro", IDLike: []string{"arch"}}, match{arch: true}},
		{"opensuse-leap", OSRelease{ID: "opensuse-leap", IDLike: []string{"suse", "opensuse"}}, match{suse: true}},
		{"opensuse-tumbleweed", OSRelease{ID: "opensuse-tumbleweed", IDLike: []string{"suse"}}, match{suse: true}},
		{"sles", OSRelease{ID: "sles"}, match{suse: true}},
		{"void", OSRelease{ID: "void"}, match{void_: true}},
		{"alpine", OSRelease{ID: "alpine"}, match{}},
		{"slackware", OSRelease{ID: "slackware"}, match{}},
		{"empty", OSRelease{}, match{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := match{
				debian: c.rel.IsDebianLike(),
				fedora: c.rel.IsFedoraLike(),
				arch:   c.rel.IsArchLike(),
				suse:   c.rel.IsSuseLike(),
				void_:  c.rel.IsVoidLike(),
			}
			if got != c.want {
				t.Errorf("families(%+v) = %+v, want %+v", c.rel, got, c.want)
			}
		})
	}
}

func TestToolExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !ToolExists(file) {
		t.Error("regular file must exist")
	}
	if ToolExists(dir) {
		t.Error("directories are not tools")
	}
	if ToolExists(filepath.Join(dir, "missing")) {
		t.Error("missing path must not exist")
	}
}

func TestMissingByExitStatus(t *testing.T) {
	f := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if c.Name != "/bin/probe" {
			return runner.Result{}, false
		}
		if c.Args[0] == "present" {
			return runner.Result{ExitCode: 0}, true
		}
		return runner.Result{ExitCode: 1}, true
	}}
	pkgs := []Package{{Name: "present", What: "x"}, {Name: "absent", What: "y"}}
	missing, err := MissingByExitStatus(context.Background(), f, pkgs, func(name string) (string, []string) {
		return "/bin/probe", []string{name}
	})
	if err != nil {
		t.Fatalf("MissingByExitStatus: %v", err)
	}
	if len(missing) != 1 || missing[0].Name != "absent" {
		t.Errorf("missing = %+v, want only absent", missing)
	}
}

func TestMissingByExitStatusTreatsErrExitAsMissing(t *testing.T) {
	// ConcreteRunner reports non-zero exits as ErrExit; that is a
	// missing package, not a failure.
	f := &runner.FakeRunner{Errs: map[string]error{
		"/bin/probe absent": runner.ErrExit{ExitCode: 2, Cmd: "/bin/probe absent"},
	}}
	missing, err := MissingByExitStatus(context.Background(), f, []Package{{Name: "absent"}}, func(string) (string, []string) {
		return "/bin/probe", []string{"absent"}
	})
	if err != nil {
		t.Fatalf("MissingByExitStatus: %v", err)
	}
	if len(missing) != 1 || missing[0].Name != "absent" {
		t.Errorf("missing = %+v", missing)
	}
}

func TestMissingByExitStatusStartFailureIsFatal(t *testing.T) {
	f := &runner.FakeRunner{Errs: map[string]error{
		"/bin/probe absent": errors.New("exec: no such file"),
	}}
	_, err := MissingByExitStatus(context.Background(), f, []Package{{Name: "absent"}}, func(string) (string, []string) {
		return "/bin/probe", []string{"absent"}
	})
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("err = %v, want a probe failure naming the package", err)
	}
}

func TestRunSudoRunsVisibly(t *testing.T) {
	f := &runner.FakeRunner{}
	if err := RunSudo(context.Background(), f, []string{"dnf", "install", "cmake"}); err != nil {
		t.Fatalf("RunSudo: %v", err)
	}
	calls := f.Called()
	if len(calls) != 1 || calls[0].Name != "sudo" || !calls[0].Interactive {
		t.Fatalf("calls = %+v, want one interactive sudo", calls)
	}
	if strings.Join(calls[0].Args, " ") != "dnf install cmake" {
		t.Errorf("args = %v", calls[0].Args)
	}
}

func TestRunSudoErrorsNameTheFullCommand(t *testing.T) {
	f := &runner.FakeRunner{Default: runner.Result{ExitCode: 100}}
	err := RunSudo(context.Background(), f, []string{"apt-get", "install", "g++"})
	if err == nil || !strings.Contains(err.Error(), "sudo apt-get install g++ exited with code 100") {
		t.Fatalf("err = %v, want the full command and exit code", err)
	}

	f2 := &runner.FakeRunner{Errs: map[string]error{
		"sudo dnf install cmake": runner.ErrExit{ExitCode: 1, Cmd: "sudo dnf install cmake"},
	}}
	err = RunSudo(context.Background(), f2, []string{"dnf", "install", "cmake"})
	if err == nil || !strings.Contains(err.Error(), "sudo dnf install cmake exited with code 1") {
		t.Fatalf("err = %v, want ErrExit mapped to the full command", err)
	}

	f3 := &runner.FakeRunner{Errs: map[string]error{
		"sudo zypper install cmake": errors.New("exec: sudo not found"),
	}}
	err = RunSudo(context.Background(), f3, []string{"zypper", "install", "cmake"})
	if err == nil || !strings.Contains(err.Error(), "run sudo zypper install cmake") {
		t.Fatalf("err = %v, want a start-failure error naming the command", err)
	}
}
