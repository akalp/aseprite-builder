package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/akalp/aseprite-builder/internal/app"
	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/state"
)

// packageFakePlatform extends cliFakePlatform with the system-package
// provider/installer capabilities (the Linux platform's optional
// interfaces).
type packageFakePlatform struct {
	cliFakePlatform
	missing      []packages.Package
	command      string
	installCalls [][]packages.Package
	installErr   error
	afterInstall []packages.Package
}

func (f *packageFakePlatform) SystemPackages(context.Context) (packages.Report, error) {
	return packages.Report{Manager: "apt", Missing: f.missing, Command: f.command}, nil
}

func (f *packageFakePlatform) InstallSystemPackages(_ context.Context, pkgs []packages.Package) error {
	f.installCalls = append(f.installCalls, pkgs)
	if f.installErr != nil {
		return f.installErr
	}
	f.missing = f.afterInstall
	return nil
}

func missingPlatform() *packageFakePlatform {
	return &packageFakePlatform{
		missing: []packages.Package{
			{Name: "g++", What: "C++ compiler"},
			{Name: "cmake", What: "build system"},
		},
		command: "sudo apt-get install g++ cmake",
	}
}

func TestEnsureSystemPackagesNonTTYShowsCommandAndStops(t *testing.T) {
	plat := missingPlatform()
	svc := newCLITestService(t, plat)
	c, _, stderr := newFakeCLI(t, svc)

	ok, err := c.ensureSystemPackages(context.Background(), svc)
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v, want stop with no error", ok, err)
	}
	if len(plat.installCalls) != 0 {
		t.Fatal("sudo must never run without explicit consent")
	}
	out := stderr.String()
	for _, want := range []string{"g++ (C++ compiler)", "sudo apt-get install g++ cmake", "Nothing was changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr missing %q:\n%s", want, out)
		}
	}
}

func TestEnsureSystemPackagesDeclinedDoesNotInstall(t *testing.T) {
	plat := missingPlatform()
	svc := newCLITestService(t, plat)
	c, _, stderr := newFakeCLI(t, svc)
	c.ForceTTY = true
	c.Stdin = strings.NewReader("n\n")

	ok, err := c.ensureSystemPackages(context.Background(), svc)
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v, want stop with no error", ok, err)
	}
	if len(plat.installCalls) != 0 {
		t.Fatal("declining must not run the installer")
	}
	if !strings.Contains(stderr.String(), "sudo apt-get install g++ cmake") {
		t.Errorf("the exact command must still be shown:\n%s", stderr.String())
	}
}

func TestEnsureSystemPackagesConsentedRunsInstallerAndRechecks(t *testing.T) {
	plat := missingPlatform()
	plat.afterInstall = nil // packages land
	svc := newCLITestService(t, plat)
	c, _, stderr := newFakeCLI(t, svc)
	c.ForceTTY = true
	c.Stdin = strings.NewReader("y\n")

	ok, err := c.ensureSystemPackages(context.Background(), svc)
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v, want proceed", ok, err)
	}
	if len(plat.installCalls) != 1 {
		t.Fatalf("install calls = %d, want 1", len(plat.installCalls))
	}
	got := packages.Names(plat.installCalls[0])
	if strings.Join(got, ",") != "g++,cmake" {
		t.Errorf("installed packages = %v", got)
	}
	if !strings.Contains(stderr.String(), "✓ System packages installed.") {
		t.Errorf("stderr missing confirmation:\n%s", stderr.String())
	}
}

func TestEnsureSystemPackagesStillMissingAfterInstallFails(t *testing.T) {
	plat := missingPlatform()
	plat.afterInstall = []packages.Package{{Name: "cmake", What: "build system"}}
	svc := newCLITestService(t, plat)
	c, _, _ := newFakeCLI(t, svc)
	c.ForceTTY = true
	c.Stdin = strings.NewReader("y\n")

	ok, err := c.ensureSystemPackages(context.Background(), svc)
	if ok || err == nil || !strings.Contains(err.Error(), "still missing") {
		t.Fatalf("ok=%v err=%v, want a still-missing failure", ok, err)
	}
}

func TestEnsureSystemPackagesInstallerErrorSurfaces(t *testing.T) {
	plat := missingPlatform()
	plat.installErr = errors.New("sudo: a terminal is required")
	svc := newCLITestService(t, plat)
	c, _, _ := newFakeCLI(t, svc)
	c.ForceTTY = true
	c.Stdin = strings.NewReader("y\n")

	ok, err := c.ensureSystemPackages(context.Background(), svc)
	if ok || err == nil || !strings.Contains(err.Error(), "terminal is required") {
		t.Fatalf("ok=%v err=%v, want the installer error", ok, err)
	}
}

func TestEnsureSystemPackagesNoopWithoutBackend(t *testing.T) {
	// A platform without the system-package interfaces (macOS/Windows):
	// the pre-flight is a silent no-op.
	svc := newCLITestService(t, &cliFakePlatform{})
	c, _, stderr := newFakeCLI(t, svc)
	ok, err := c.ensureSystemPackages(context.Background(), svc)
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v, want no-op proceed", ok, err)
	}
	if stderr.Len() != 0 {
		t.Errorf("no output expected, got %q", stderr.String())
	}
}

func TestInstallYesDoesNotApproveSystemPackages(t *testing.T) {
	// --yes skips builder prompts only; it must never approve a sudo
	// package installation (spec §16).
	plat := missingPlatform()
	svc := newCLITestService(t, plat)
	c, _, stderr := newFakeCLI(t, svc)

	code := c.Run(context.Background(), []string{"install", "--yes"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (missing packages stop the flow)", code)
	}
	if len(plat.installCalls) != 0 {
		t.Fatal("--yes must not run the privileged installer")
	}
	if !strings.Contains(stderr.String(), "sudo apt-get install g++ cmake") {
		t.Errorf("the exact command must be shown:\n%s", stderr.String())
	}
}

func TestUpdateYesDoesNotApproveSystemPackages(t *testing.T) {
	plat := missingPlatform()
	svc := newCLITestService(t, plat)
	seedInstallState(t, svc)
	c, _, stderr := newFakeCLI(t, svc)

	code := c.Run(context.Background(), []string{"update", "--yes"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if len(plat.installCalls) != 0 {
		t.Fatal("--yes must not run the privileged installer")
	}
	if !strings.Contains(stderr.String(), "sudo apt-get install g++ cmake") {
		t.Errorf("the exact command must be shown:\n%s", stderr.String())
	}
}

func TestUpdateWithoutInstallationDoesNotOfferPackages(t *testing.T) {
	// With nothing installed, update must report "install first" rather
	// than pushing system packages for a build that will not happen.
	plat := missingPlatform()
	svc := newCLITestService(t, plat)
	c, _, stderr := newFakeCLI(t, svc)

	code := c.Run(context.Background(), []string{"update", "--yes"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if len(plat.installCalls) != 0 {
		t.Fatal("no installer call may happen without an installation")
	}
	if strings.Contains(stderr.String(), "sudo apt-get install") {
		t.Errorf("no package prompt without an installation:\n%s", stderr.String())
	}
}

// seedInstallState records an existing installation so update/rebuild
// preflights run.
func seedInstallState(t *testing.T, svc *app.Service) {
	t.Helper()
	store := state.Store{Dir: svc.Layout().State}
	err := store.SaveInstall(state.InstallState{
		AsepriteVersion: "1.3.18.6",
		TagName:         "v1.3.18.6",
		Channel:         "stable",
		AppPath:         "/fake/Applications/Aseprite.app",
		InstalledAt:     time.Unix(0, 0).UTC(),
		WorkspacePath:   "/fake/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
}
