package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/app"
	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/state"
)

// cliFakePlatform implements platform.Platform for CLI tests.
type cliFakePlatform struct {
	failRequirements    bool
	installed           string
	allowReplaceUnowned bool
}

func (f *cliFakePlatform) Detect(context.Context) (platform.SystemInfo, error) {
	return platform.SystemInfo{OS: "darwin", OSVersion: "15.2", Arch: "arm64", Supported: true}, nil
}

func (f *cliFakePlatform) Requirements(context.Context) ([]platform.Check, error) {
	if f.failRequirements {
		return []platform.Check{
			{Name: "Apple toolchain", OK: false, Fix: "xcode-select --install"},
		}, nil
	}
	return []platform.Check{{Name: "Apple toolchain", OK: true, Detail: "/fake/CLT"}}, nil
}

func (f *cliFakePlatform) Prepare(context.Context) error { return nil }

func (f *cliFakePlatform) Install(_ context.Context, out platform.BuildOutput) (platform.InstallOutcome, error) {
	f.installed = out.Version
	return platform.InstallOutcome{AppPath: out.AppPath}, nil
}

func (f *cliFakePlatform) Launch(context.Context) error { return nil }

func (f *cliFakePlatform) Uninstall(context.Context, platform.UninstallOptions) error { return nil }

func (f *cliFakePlatform) InstalledVersion(context.Context) (string, error) { return f.installed, nil }

func (f *cliFakePlatform) DefaultInstallPath() string { return "/fake/Applications/Aseprite.app" }

func (f *cliFakePlatform) SetAllowReplaceUnowned(v bool) { f.allowReplaceUnowned = v }

// cliFakeReleases serves no releases or one pinned release.
type cliFakeReleases struct{ releases []aseprite.Release }

func (r cliFakeReleases) Resolve(ctx context.Context, c aseprite.Channel) (aseprite.Release, error) {
	rels, _ := r.List(ctx, c)
	if len(rels) == 0 {
		return aseprite.Release{}, errors.New("no releases")
	}
	return rels[0], nil
}

func (r cliFakeReleases) List(context.Context, aseprite.Channel) ([]aseprite.Release, error) {
	return r.releases, nil
}

func newCLITestService(t *testing.T, plat platform.Platform) *app.Service {
	t.Helper()
	layout, err := paths.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	svc, err := app.New(app.Options{
		BuilderVersion: "test",
		Layout:         layout,
		Platform:       plat,
		Releases:       cliFakeReleases{},
		Runner:         &runner.FakeRunner{},
		Store:          state.Store{Dir: layout.State},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newFakeCLI(t *testing.T, svc *app.Service) (*CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	c := &CLI{
		Stdout:         stdout,
		Stderr:         stderr,
		Stdin:          strings.NewReader("\n"), // EOF → confirm defaults to no
		BuilderVersion: "test",
		ServiceFactory: func(ctx context.Context, bv string, verifyPlatform bool) (*app.Service, error) {
			return svc, nil
		},
	}
	return c, stdout, stderr
}

func TestUnknownCommand(t *testing.T) {
	c, _, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	if code := c.Run(context.Background(), []string{"bogus"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestHelpCommand(t *testing.T) {
	c, stdout, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	if code := c.Run(context.Background(), []string{"help"}); code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "install") {
		t.Error("usage should mention install")
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	c, stdout, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	if code := c.Run(context.Background(), nil); code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "uninstall") {
		t.Error("usage should list commands")
	}
}

func TestVersionCommand(t *testing.T) {
	c, stdout, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	if code := c.Run(context.Background(), []string{"version"}); code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "aseprite-builder") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestInstallCancelledByEOFConfirm(t *testing.T) {
	c, _, stderr := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	// Stdin EOF → confirm reads empty → defaults to no → cancelled.
	code := c.Run(context.Background(), []string{"install"})
	if code != 1 {
		t.Errorf("exit = %d, want 1 (cancelled)", code)
	}
	if !strings.Contains(stderr.String(), "cancelled") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestInstallRequirementsFailure(t *testing.T) {
	plat := &cliFakePlatform{failRequirements: true}
	c, _, stderr := newFakeCLI(t, newCLITestService(t, plat))
	c.Stdin = strings.NewReader("y\n") // answer yes to the confirmation
	code := c.Run(context.Background(), []string{"install", "--yes"})
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "missing some requirements") {
		t.Errorf("stderr = %q", out)
	}
	if !strings.Contains(out, "xcode-select --install") {
		t.Errorf("stderr should contain the fix hint; got %q", out)
	}
	// Raw internal errors must not be the primary message.
	if strings.Contains(out, "exit status") {
		t.Errorf("raw error leaked as primary message: %q", out)
	}
}

func TestInstallRejectsUnknownChannel(t *testing.T) {
	c, _, stderr := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	code := c.Run(context.Background(), []string{"install", "--yes", "--channel", "beta"})
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unsupported channel") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestInstallDevChannelRoutesToCommitSource(t *testing.T) {
	// --channel dev --version <sha> must never hit the stable release
	// provider (this harness serves no releases); it attempts the git
	// checkout and then fails on the empty fixture tree with the
	// unknown-contract message. That proves the development path ran.
	c, _, stderr := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	code := c.Run(context.Background(), []string{
		"install", "--yes", "--channel", "dev",
		"--version", "abc1234def5678901234567890abcdef12345678",
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "does not recognize") {
		t.Errorf("stderr = %q, want the unknown-contract message", out)
	}
	if strings.Contains(out, "unsupported channel") || strings.Contains(out, "no releases") {
		t.Errorf("dev install must not consult the stable release provider: %q", out)
	}
}

func TestInstallDevChannelRequiresCommit(t *testing.T) {
	c, _, stderr := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	code := c.Run(context.Background(), []string{"install", "--yes", "--channel", "dev"})
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--channel dev --version") {
		t.Errorf("stderr = %q, want the explicit-commit guidance", stderr.String())
	}
}

func TestDoctorJSON(t *testing.T) {
	c, stdout, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	code := c.Run(context.Background(), []string{"doctor", "--json"})
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout not JSON: %v (%q)", err, stdout.String())
	}
	if _, ok := report["system"]; !ok {
		t.Errorf("report missing system key: %s", stdout.String())
	}
}

func TestDoctorHumanOutputAligned(t *testing.T) {
	c, stdout, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	if code := c.Run(context.Background(), []string{"doctor"}); code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "Everything looks good.") {
		t.Errorf("stdout = %q", stdout.String())
	}
	// Column alignment: the ✓/✗ marks must be followed by a consistent gap.
	lines := strings.Split(stdout.String(), "\n")
	countMark := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "✓ ") {
			countMark++
		}
		if strings.HasPrefix(line, "✗") {
			t.Errorf("healthy report must not contain failures: %q", line)
		}
	}
	if countMark < 5 {
		t.Errorf("expected several check lines, got %d in:\n%s", countMark, stdout.String())
	}
}

func TestFriendlyMessageUnknownContract(t *testing.T) {
	err := fmt.Errorf("wrap: %w", &app.UnknownContractError{
		Reason: errors.New("missing build.sh"),
	})
	got := app.FriendlyMessage(err)
	if !strings.Contains(got, "does not recognize") {
		t.Errorf("message = %q", got)
	}
	if strings.Contains(got, "wrap:") {
		t.Error("wrapped internal detail should not lead the message")
	}
}

func TestFriendlyMessageUpstreamBuildError(t *testing.T) {
	e := &aseprite.UpstreamBuildError{ExitCode: 2}
	got := app.FriendlyMessage(e)
	if !strings.Contains(got, "could not be compiled") {
		t.Errorf("message = %q", got)
	}
}

func TestFriendlyMessageRequirements(t *testing.T) {
	err := app.NewSystemRequirementsErrorForTest()
	got := app.FriendlyMessage(err)
	if !strings.Contains(got, "missing some requirements") {
		t.Errorf("message = %q", got)
	}
}

// Verify the fake zip helper compiles and works (used by pipeline tests
// elsewhere; kept minimal here).
func TestBuildZipBytesSmoke(t *testing.T) {
	data := zipBytesForTest(t, map[string]string{"a.txt": "hi"})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("bad zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "a.txt" {
		t.Errorf("zip contents = %+v", zr.File)
	}
}

func zipBytesForTest(t *testing.T, files map[string]string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fo, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fo.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Help behavior tests implementing the approved help policy:
//   - top-level help shows every command's flags inline, exit 0
//   - `<cmd> --help` prints that command's flags, exit 0 (no service
//     is constructed — help must never hit the network)
//   - `help <cmd>` mirrors `<cmd> --help`
//   - an unknown flag prints error + usage on stderr, exit 2.

func panicFactory(t *testing.T) func(context.Context, string, bool) (*app.Service, error) {
	t.Helper()
	return func(context.Context, string, bool) (*app.Service, error) {
		t.Fatal("service must not be constructed while rendering help")
		return nil, nil
	}
}

func TestTopLevelHelpListsFlagsInline(t *testing.T) {
	c, stdout, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	if code := c.Run(context.Background(), []string{"--help"}); code != 0 {
		t.Errorf("exit = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"install", "uninstall", "doctor",
		"--version", "--yes", "--force", "--keep-workspace",
		"--previous", "--purge-sources", "--purge-tools", "--purge-skia", "--purge-logs",
		"--json", "per-command details",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("top-level help missing %q:\n%s", want, out)
		}
	}
}

func TestUninstallHelpShowsFlags(t *testing.T) {
	c, stdout, stderr := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	c.ServiceFactory = panicFactory(t) // help must not build a service
	if code := c.Run(context.Background(), []string{"uninstall", "--help"}); code != 0 {
		t.Errorf("exit = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"--force", "--previous", "--purge-logs", "Usage:", "Flags:"} {
		if !strings.Contains(out, want) {
			t.Errorf("uninstall help missing %q:\n%s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("help output must go to stdout, stderr = %q", stderr.String())
	}
}

func TestHelpSubcommandMirrorsCommandHelp(t *testing.T) {
	c, stdout, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	if code := c.Run(context.Background(), []string{"help", "uninstall"}); code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "--purge-tools") {
		t.Errorf("help uninstall output:\n%s", stdout.String())
	}
}

func TestUnknownFlagPrintsUsageOnStderr(t *testing.T) {
	c, stdout, stderr := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	c.ServiceFactory = panicFactory(t)
	if code := c.Run(context.Background(), []string{"uninstall", "--oops"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("usage for errors belongs on stderr; stdout = %q", stdout.String())
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "-oops") || !strings.Contains(errOut, "--force") {
		t.Errorf("stderr = %q, want error + usage", errOut)
	}
	// The duplicated FlagSet-native help must not leak into stderr.
	if strings.Contains(errOut, "Usage of uninstall:") {
		t.Errorf("flag's automatic help duplicated on stderr:\n%s", errOut)
	}
}

func TestEmptyArgsShowFullUsageWithFlags(t *testing.T) {
	c, stdout, _ := newFakeCLI(t, newCLITestService(t, &cliFakePlatform{}))
	if code := c.Run(context.Background(), nil); code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "--purge-skia") {
		t.Errorf("empty-args usage must show flags too:\n%s", stdout.String())
	}
}

// CLI help renders every flag exactly once per command listing.
func TestFlagLinesRenderDefaults(t *testing.T) {
	fs, _ := uninstallFlags()
	lines := flagLines(fs)
	if len(lines) != 6 {
		t.Fatalf("lines = %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], "--force") || !strings.Contains(lines[0], "prove it installed") {
		t.Errorf("line0 = %q", lines[0])
	}
	// Booleans render without a type suffix.
	if strings.Contains(lines[1], "bool") {
		t.Errorf("bool flag line should not carry a type: %q", lines[1])
	}
}
