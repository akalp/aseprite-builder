package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/state"
)

const testDevCommit = "abc1234def5678901234567890abcdef12345678"

func TestInstallDevChannelReusesCheckoutAndPersistsCommit(t *testing.T) {
	svc, plat, fk, sourcesDir := newTestService(t)
	short := aseprite.ShortCommit(testDevCommit)
	srcDir := filepath.Join(sourcesDir, "aseprite-dev-"+short)
	materializeSourceFixture(t, srcDir)
	wireDevBuildFake(t, fk, testDevCommit, "dev-"+short)

	result, err := svc.Install(context.Background(), InstallOptions{
		Channel: aseprite.ChannelDev,
		Commit:  testDevCommit,
	}, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Version != "dev-"+short {
		t.Errorf("Version = %q, want dev-%s", result.Version, short)
	}
	if plat.installed != result.Version {
		t.Errorf("platform installed = %q, want %q", plat.installed, result.Version)
	}
	if result.SkiaTag != "m124-08a5439a6b" {
		t.Errorf("SkiaTag = %q", result.SkiaTag)
	}

	st, ok, err := svc.CurrentInstall()
	if err != nil || !ok {
		t.Fatalf("CurrentInstall: ok=%v err=%v", ok, err)
	}
	if st.Channel != string(aseprite.ChannelDev) || st.CommitSHA != testDevCommit {
		t.Errorf("state = %+v, want dev channel + commit", st)
	}
	if st.SourceSHA256 != "" {
		t.Errorf("dev installs have no archive digest, got %q", st.SourceSHA256)
	}
	if st.AsepriteVersion != result.Version {
		t.Errorf("state version = %q, want %q", st.AsepriteVersion, result.Version)
	}

	// The existing checkout must be reused: no clone may run.
	for _, c := range fk.Called() {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "clone" {
			t.Errorf("existing checkout must be reused, got %v", c)
		}
	}
}

func TestInstallDevChannelRequiresExplicitCommit(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.Install(context.Background(), InstallOptions{Channel: aseprite.ChannelDev}, nil)
	if !errors.Is(err, ErrDevCommitRequired) {
		t.Fatalf("err = %v, want ErrDevCommitRequired", err)
	}
	if _, has, _ := svc.CurrentInstall(); has {
		t.Error("no install state must exist after a rejected dev install")
	}
}

func TestInstallDevChannelRejectsMalformedCommit(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.Install(context.Background(), InstallOptions{
		Channel: aseprite.ChannelDev,
		Commit:  "not-a-commit!",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid development commit") {
		t.Fatalf("err = %v, want an invalid-commit error", err)
	}
	if errors.Is(err, ErrDevCommitRequired) {
		t.Error("a malformed commit is not the same as a missing one")
	}
}

func TestInstallDevChannelVersionFlagCarriesCommit(t *testing.T) {
	// The CLI convention: --channel dev --version <sha>. The app maps
	// Version to Commit for the dev channel.
	svc, _, fk, sourcesDir := newTestService(t)
	short := aseprite.ShortCommit(testDevCommit)
	srcDir := filepath.Join(sourcesDir, "aseprite-dev-"+short)
	materializeSourceFixture(t, srcDir)
	wireDevBuildFake(t, fk, testDevCommit, "dev-"+short)

	result, err := svc.Install(context.Background(), InstallOptions{
		Channel: aseprite.ChannelDev,
		Version: testDevCommit,
	}, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Version != "dev-"+short {
		t.Errorf("Version = %q, want dev-%s", result.Version, short)
	}
}

func TestUpdateDevChannelIsRejected(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if err := svc.opts.Store.SaveInstall(state.InstallState{
		AsepriteVersion: "dev-" + aseprite.ShortCommit(testDevCommit),
		AppPath:         "/fake/Applications/Aseprite.app",
		Channel:         string(aseprite.ChannelDev),
		CommitSHA:       testDevCommit,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Update(context.Background(), InstallOptions{}, nil)
	if !errors.Is(err, ErrDevUpdateUnsupported) {
		t.Fatalf("err = %v, want ErrDevUpdateUnsupported", err)
	}
}

func TestRebuildDevChannelUsesStoredCommit(t *testing.T) {
	svc, _, fk, sourcesDir := newTestService(t)
	short := aseprite.ShortCommit(testDevCommit)
	srcDir := filepath.Join(sourcesDir, "aseprite-dev-"+short)
	materializeSourceFixture(t, srcDir)
	wireDevBuildFake(t, fk, testDevCommit, "dev-"+short)

	if err := svc.opts.Store.SaveInstall(state.InstallState{
		AsepriteVersion: "dev-" + short,
		AppPath:         "/fake/Applications/Aseprite.app",
		Channel:         string(aseprite.ChannelDev),
		CommitSHA:       testDevCommit,
		WorkspacePath:   srcDir,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Rebuild(context.Background(), RebuildOptions{}, nil)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if result.Version != "dev-"+short {
		t.Errorf("Version = %q, want dev-%s", result.Version, short)
	}
	for _, c := range fk.Called() {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "clone" {
			t.Errorf("rebuild must reuse the stored checkout, got %v", c)
		}
	}
}

func TestDevChannelFriendlyMessages(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrDevCommitRequired, "--channel dev --version"},
		{ErrDevUpdateUnsupported, "development commit"},
		{fmt.Errorf("outer: %w", aseprite.ErrGitRequired), "requires Git"},
	}
	for _, c := range cases {
		if got := FriendlyMessage(c.err); !strings.Contains(got, c.want) {
			t.Errorf("FriendlyMessage(%v) = %q, want %q in the text", c.err, got, c.want)
		}
	}
}

func TestResolveDevReleaseValidation(t *testing.T) {
	rel, err := resolveDevRelease("  " + testDevCommit + "  ")
	if err != nil {
		t.Fatalf("resolveDevRelease: %v", err)
	}
	if rel.Commit != testDevCommit || rel.TagName != "dev-abc1234" {
		t.Errorf("release = %+v", rel)
	}
	if !rel.DevCommit() || rel.BuildVersion() != "dev-abc1234" {
		t.Errorf("release helpers = %+v / %q", rel, rel.BuildVersion())
	}
	if _, err := resolveDevRelease(""); !errors.Is(err, ErrDevCommitRequired) {
		t.Errorf("empty commit err = %v", err)
	}
	if _, err := resolveDevRelease("zz"); err == nil {
		t.Error("malformed commit must fail")
	}
}
