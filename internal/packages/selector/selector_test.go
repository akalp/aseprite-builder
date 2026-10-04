package selector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/packages/apt"
	"github.com/akalp/aseprite-builder/internal/packages/dnf"
	"github.com/akalp/aseprite-builder/internal/packages/pacman"
	"github.com/akalp/aseprite-builder/internal/packages/xbps"
	"github.com/akalp/aseprite-builder/internal/packages/zypper"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// fakeManagers fabricates tool files for the listed base names and
// points every manager at either the fabricated file or a nonexistent
// path, so Detect is deterministic on any host.
func fakeManagers(t *testing.T, present ...string) map[packages.ManagerName]packages.Manager {
	t.Helper()
	dir := t.TempDir()
	have := map[string]bool{}
	for _, name := range present {
		have[name] = true
	}
	tool := func(name string) string {
		p := filepath.Join(dir, name)
		if have[name] {
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	r := &runner.FakeRunner{}
	return map[packages.ManagerName]packages.Manager{
		packages.ManagerApt:    apt.Platform{Runner: r, APTGet: tool("apt-get"), DpkgQuery: tool("dpkg-query")},
		packages.ManagerDnf:    dnf.Platform{Runner: r, DNF: tool("dnf"), RPM: tool("rpm")},
		packages.ManagerPacman: pacman.Platform{Runner: r, Pacman: tool("pacman")},
		packages.ManagerZypper: zypper.Platform{Runner: r, Zypper: tool("zypper"), RPM: tool("rpm")},
		packages.ManagerXbps:   xbps.Platform{Runner: r, XbpsInstall: tool("xbps-install"), XbpsQuery: tool("xbps-query")},
	}
}

func assertNames(t *testing.T, got, want []packages.ManagerName) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
	}
}

func TestCandidatesByDistribution(t *testing.T) {
	cases := []struct {
		name string
		rel  packages.OSRelease
		want []packages.ManagerName
	}{
		{"ubuntu", packages.OSRelease{ID: "ubuntu", IDLike: []string{"debian"}}, []packages.ManagerName{packages.ManagerApt}},
		{"debian", packages.OSRelease{ID: "debian"}, []packages.ManagerName{packages.ManagerApt}},
		{"mint", packages.OSRelease{ID: "linuxmint", IDLike: []string{"ubuntu"}}, []packages.ManagerName{packages.ManagerApt}},
		{"fedora", packages.OSRelease{ID: "fedora"}, []packages.ManagerName{packages.ManagerDnf}},
		{"rocky", packages.OSRelease{ID: "rocky", IDLike: []string{"rhel", "centos", "fedora"}}, []packages.ManagerName{packages.ManagerDnf}},
		{"arch", packages.OSRelease{ID: "arch"}, []packages.ManagerName{packages.ManagerPacman}},
		{"manjaro", packages.OSRelease{ID: "manjaro", IDLike: []string{"arch"}}, []packages.ManagerName{packages.ManagerPacman}},
		{"opensuse-leap", packages.OSRelease{ID: "opensuse-leap", IDLike: []string{"suse", "opensuse"}}, []packages.ManagerName{packages.ManagerZypper}},
		{"opensuse-tumbleweed", packages.OSRelease{ID: "opensuse-tumbleweed", IDLike: []string{"suse"}}, []packages.ManagerName{packages.ManagerZypper}},
		{"void", packages.OSRelease{ID: "void"}, []packages.ManagerName{packages.ManagerXbps}},
		{"unknown", packages.OSRelease{ID: "alpine"}, fallbackProbeOrder},
		{"empty", packages.OSRelease{}, fallbackProbeOrder},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertNames(t, Candidates(c.rel), c.want)
		})
	}
}

func TestCandidatesReturnsFreshSlices(t *testing.T) {
	// Mutating a returned fallback slice must not corrupt future calls.
	got := Candidates(packages.OSRelease{ID: "alpine"})
	got[0] = packages.ManagerXbps
	assertNames(t, Candidates(packages.OSRelease{ID: "alpine"}), fallbackProbeOrder)
}

func TestManagerPrefersFamilyOverProbeOrder(t *testing.T) {
	// Both apt and dnf exist; a Fedora release must select dnf (its
	// family), not apt (first in probe order).
	avail := fakeManagers(t, "apt-get", "dpkg-query", "dnf", "rpm")
	mgr, name, err := Manager(context.Background(), packages.OSRelease{ID: "fedora"}, avail)
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}
	if name != "dnf" {
		t.Fatalf("name = %q, want dnf", name)
	}
	if _, ok := mgr.(dnf.Platform); !ok {
		t.Errorf("manager type = %T, want dnf.Platform", mgr)
	}
}

func TestManagerAssignsEachFamily(t *testing.T) {
	cases := []struct {
		rel   packages.OSRelease
		tools []string
		name  string
	}{
		{packages.OSRelease{ID: "ubuntu"}, []string{"apt-get", "dpkg-query"}, "apt"},
		{packages.OSRelease{ID: "fedora"}, []string{"dnf", "rpm"}, "dnf"},
		{packages.OSRelease{ID: "arch"}, []string{"pacman"}, "pacman"},
		{packages.OSRelease{ID: "opensuse-leap"}, []string{"zypper", "rpm"}, "zypper"},
		{packages.OSRelease{ID: "void"}, []string{"xbps-install", "xbps-query"}, "xbps"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, name, err := Manager(context.Background(), c.rel, fakeManagers(t, c.tools...))
			if err != nil {
				t.Fatalf("Manager: %v", err)
			}
			if name != c.name {
				t.Errorf("name = %q, want %q", name, c.name)
			}
		})
	}
}

func TestManagerAmbiguousProbesInOrder(t *testing.T) {
	// An unknown distribution with dnf and pacman available: apt (first
	// in probe order) is absent, so dnf wins over pacman.
	avail := fakeManagers(t, "dnf", "rpm", "pacman")
	_, name, err := Manager(context.Background(), packages.OSRelease{ID: "alpine"}, avail)
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}
	if name != "dnf" {
		t.Errorf("name = %q, want dnf (probe order)", name)
	}
}

func TestManagerKnownFamilyNeverFallsBackToOther(t *testing.T) {
	// Fedora without dnf: fail, never silently use a different family.
	avail := fakeManagers(t, "apt-get", "dpkg-query")
	_, _, err := Manager(context.Background(), packages.OSRelease{ID: "fedora"}, avail)
	if !errors.Is(err, packages.ErrUnsupportedDistro) {
		t.Fatalf("err = %v, want ErrUnsupportedDistro", err)
	}
}

func TestManagerUnsupportedWithoutAnyBinary(t *testing.T) {
	_, _, err := Manager(context.Background(), packages.OSRelease{ID: "alpine"}, fakeManagers(t))
	if !errors.Is(err, packages.ErrUnsupportedDistro) {
		t.Fatalf("err = %v, want ErrUnsupportedDistro", err)
	}
}

func TestManagerSkipsNilEntries(t *testing.T) {
	avail := fakeManagers(t, "dnf", "rpm")
	delete(avail, packages.ManagerDnf)
	_, _, err := Manager(context.Background(), packages.OSRelease{ID: "fedora"}, avail)
	if !errors.Is(err, packages.ErrUnsupportedDistro) {
		t.Fatalf("err = %v, want ErrUnsupportedDistro", err)
	}
}

func TestManagerCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Manager(ctx, packages.OSRelease{ID: "fedora"}, fakeManagers(t, "dnf", "rpm"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
