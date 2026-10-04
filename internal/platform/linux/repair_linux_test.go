//go:build linux

package linux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

func TestRepairTargetedRecreatesIntegration(t *testing.T) {
	f := &runner.FakeRunner{Match: versionFaker("1.3.18.6")}
	p := newTestPlatform(t, f)
	out := fakeInstallPayload(t, t.TempDir(), "1.3.18.6")
	out.IconsDir = fixtureIcons(t)
	out.DesktopFile = fixtureDesktopFile(t)
	if _, err := p.Install(context.Background(), out); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Break the integration: remove the CLI symlink and desktop entry.
	link := filepath.Join(p.CLIBinDir, exeName)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p.desktopEntryPath()); err != nil {
		t.Fatal(err)
	}

	actions, err := p.RepairTargeted(context.Background(), platform.RepairRequest{
		AppPath: p.DefaultInstallPath(),
		Version: "1.3.18.6",
	})
	if err != nil {
		t.Fatalf("RepairTargeted: %v", err)
	}
	joined := strings.Join(actions, "; ")
	if !strings.Contains(joined, "CLI symlink") || !strings.Contains(joined, "desktop entry") {
		t.Errorf("actions = %v, want the symlink and desktop entry recreated", actions)
	}
	if target, err := os.Readlink(link); err != nil || target != p.exePath() {
		t.Errorf("symlink = %q, %v; want %q", target, err, p.exePath())
	}
	entry, err := os.ReadFile(p.desktopEntryPath())
	if err != nil || !strings.HasPrefix(string(entry), desktopMarker) {
		t.Errorf("desktop entry not recreated: err=%v content=%q", err, entry)
	}

	ok, detail := p.CheckIntegration()
	if !ok {
		t.Errorf("CheckIntegration = %v (%s), want valid after repair", ok, detail)
	}
}

func TestCheckIntegrationReportsIssues(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	ok, detail := p.CheckIntegration()
	if ok {
		t.Fatalf("an empty integration must report issues, got %q", detail)
	}
	for _, want := range []string{"CLI symlink missing", "desktop entry missing", "icons missing"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	}
}
