package aseprite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aseprite-builder/aseprite-builder/internal/runner"
)

// buildScriptKey matches the bash build.sh invocation in the fake runner.
func buildScriptKey(src string) string {
	return "bash " + filepath.Join(src, "build.sh") + " --auto --norun"
}

// fakeBuildOutput materializes what the upstream build would produce.
func fakeBuildOutput(t *testing.T, src, version string) {
	t.Helper()
	app := filepath.Join(src, "build", "bin", "Aseprite.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app, "Contents", "Resources", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "aseprite"), []byte("mach-o"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Resources", "data", "akey.aset"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = version
}

func TestBuildSuccess(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		// Emulating the upstream build: when the build.sh command runs,
		// materialize the artifact it would produce.
		if c.Name == "bash" && len(c.Args) == 3 && strings.HasSuffix(c.Args[0], "build.sh") {
			fakeBuildOutput(t, src, "1.3.18.6")
			return runner.Result{ExitCode: 0}, true
		}
		return runner.Result{}, false
	}}
	b := Builder{Runner: r}
	skia := filepath.Join(t.TempDir(), "skia", "skia-m124")

	out, err := b.Build(context.Background(), BuildRequest{
		SourceDir:    src,
		Contract:     Contract{BuildScript: filepath.Join(src, "build.sh"), SkiaDirName: "skia-m124", BundleOutput: "build/bin/Aseprite.app"},
		SkiaDir:      skia,
		CPU:          "arm64",
		ExtraBinDirs: []string{"/cache/cmake/bin", "/cache/ninja/bin"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantApp := filepath.Join(src, "build", "bin", "Aseprite.app")
	if out.AppPath != wantApp {
		t.Errorf("AppPath = %q, want %q", out.AppPath, wantApp)
	}
	if out.Executable != filepath.Join(wantApp, "Contents", "MacOS", "aseprite") {
		t.Errorf("Executable = %q", out.Executable)
	}
	if out.DataDir != filepath.Join(wantApp, "Contents", "Resources", "data") {
		t.Errorf("DataDir = %q", out.DataDir)
	}
}

func TestBuildSeedsUpstreamConfig(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Results: map[string]runner.Result{
		buildScriptKey(src): {ExitCode: 0},
	}}
	b := Builder{Runner: r}
	skia := filepath.Join(t.TempDir(), "skia-m124")
	_, _ = b.Build(context.Background(), BuildRequest{
		SourceDir: src,
		Contract:  Contract{BuildScript: filepath.Join(src, "build.sh"), SkiaDirName: "skia-m124"},
		SkiaDir:   skia, CPU: "arm64",
	})

	configDir := filepath.Join(src, ".build")
	userkind, err := os.ReadFile(filepath.Join(configDir, "userkind"))
	if err != nil || strings.TrimSpace(string(userkind)) != "user" {
		t.Errorf("userkind = %q, %v", userkind, err)
	}
	buildsDir, err := os.ReadFile(filepath.Join(configDir, "builds_dir"))
	if err != nil || strings.TrimSpace(string(buildsDir)) != src {
		t.Errorf("builds_dir = %q, %v", buildsDir, err)
	}
	skiaCfg, err := os.ReadFile(filepath.Join(configDir, "_skia_dir"))
	if err != nil || strings.TrimSpace(string(skiaCfg)) != skia {
		t.Errorf("_skia_dir = %q, %v (want %s)", skiaCfg, err, skia)
	}
	// Skia config file names mirror the branch variants build.sh reads.
	if _, err := os.Stat(filepath.Join(configDir, "main_skia_dir")); err != nil {
		t.Errorf("main_skia_dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "beta_skia_dir")); err != nil {
		t.Errorf("beta_skia_dir missing: %v", err)
	}
	// .deps seeding
	link := filepath.Join(src, ".deps", "skia-m124")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("skia symlink: %v", err)
	}
	if target != skia {
		t.Errorf("symlink target = %q, want %q", target, skia)
	}
}

func TestBuildEnvPrependsCachedTools(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Results: map[string]runner.Result{
		buildScriptKey(src): {ExitCode: 0},
	}}
	b := Builder{Runner: r, Log: nil}
	_, _ = b.Build(context.Background(), BuildRequest{
		SourceDir:    src,
		Contract:     Contract{BuildScript: filepath.Join(src, "build.sh")},
		ExtraBinDirs: []string{"/cache/cmake/bin"},
	})
	calls := r.Called()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	env := calls[0].Env
	if len(env) != 1 || !strings.HasPrefix(env[0], "PATH=") {
		t.Fatalf("env = %v", env)
	}
	if !strings.Contains(env[0], "/cache/cmake/bin") {
		t.Errorf("PATH = %q, want cached tool dir prepended", env[0])
	}
	// Inherited PATH is preserved after the prefix.
	if strings.HasSuffix(env[0], ":") || !strings.Contains(env[0], os.Getenv("PATH")) {
		t.Errorf("PATH = %q, inherited PATH dropped", env[0])
	}
}

func TestBuildFailureIsUpstreamError(t *testing.T) {
	src := fixtureSource(t)
	fakeBuildOutput(t, src, "1.3.18.6") // output exists but build failed
	r := &runner.FakeRunner{
		Results: map[string]runner.Result{
			buildScriptKey(src): {ExitCode: 2, Stderr: "ninja: error: something failed\n"},
		},
	}
	b := Builder{Runner: r}
	_, err := b.Build(context.Background(), BuildRequest{
		SourceDir: src,
		Contract:  Contract{BuildScript: filepath.Join(src, "build.sh")},
	})
	if err == nil {
		t.Fatal("build failure must surface as an error")
	}
	var ube *UpstreamBuildError
	if !errors.As(err, &ube) {
		t.Fatalf("err = %T %v, want UpstreamBuildError", err, err)
	}
	if ube.ExitCode != 2 {
		t.Errorf("ExitCode = %d", ube.ExitCode)
	}
	if !strings.Contains(ube.Tail, "ninja: error") {
		t.Errorf("Tail = %q", ube.Tail)
	}
}

func TestBuildMissingOutputIsContractViolation(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Results: map[string]runner.Result{
		buildScriptKey(src): {ExitCode: 0},
	}}
	b := Builder{Runner: r}
	_, err := b.Build(context.Background(), BuildRequest{
		SourceDir: src,
		Contract:  Contract{BuildScript: filepath.Join(src, "build.sh")},
	})
	var cve *ContractViolationError
	if !errors.As(err, &cve) {
		t.Fatalf("err = %T %v, want ContractViolationError", err, err)
	}
	if cve.Stage != "build output" {
		t.Errorf("Stage = %q", cve.Stage)
	}
}

func TestBuildStaleSkiaSymlinkRepointed(t *testing.T) {
	src := fixtureSource(t)
	r := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		// Emulating the upstream build: when the build.sh command runs,
		// materialize the artifact it would produce.
		if c.Name == "bash" && len(c.Args) == 3 && strings.HasSuffix(c.Args[0], "build.sh") {
			fakeBuildOutput(t, src, "1.3.18.6")
			return runner.Result{ExitCode: 0}, true
		}
		return runner.Result{}, false
	}}
	b := Builder{Runner: r}
	old := filepath.Join(t.TempDir(), "old-skia")
	fresh := filepath.Join(t.TempDir(), "new-skia")
	_, _ = b.Build(context.Background(), BuildRequest{
		SourceDir: src,
		Contract:  Contract{BuildScript: filepath.Join(src, "build.sh"), SkiaDirName: "skia-m124"},
		SkiaDir:   old,
	})
	out, err := b.Build(context.Background(), BuildRequest{
		SourceDir: src,
		Contract:  Contract{BuildScript: filepath.Join(src, "build.sh"), SkiaDirName: "skia-m124"},
		SkiaDir:   fresh,
	})
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	target, err := os.Readlink(filepath.Join(src, ".deps", "skia-m124"))
	if err != nil || target != fresh {
		t.Errorf("symlink target = %q, %v; want %q", target, err, fresh)
	}
	_ = out
}

func TestVerifyBuildOK(t *testing.T) {
	src := fixtureSource(t)
	fakeBuildOutput(t, src, "1.3.18.6")
	out := BuildOutput{
		AppPath:    filepath.Join(src, "build", "bin", "Aseprite.app"),
		Executable: filepath.Join(src, "build", "bin", "Aseprite.app", "Contents", "MacOS", "aseprite"),
		DataDir:    filepath.Join(src, "build", "bin", "Aseprite.app", "Contents", "Resources", "data"),
	}
	r := &runner.FakeRunner{Match: anyVersionFaker()}
	if err := VerifyBuild(context.Background(), r, out, "1.3.18.6"); err != nil {
		t.Errorf("VerifyBuild: %v", err)
	}
}

func TestVerifyBuildVersionMismatch(t *testing.T) {
	src := fixtureSource(t)
	fakeBuildOutput(t, src, "1.3.18.5")
	out := BuildOutput{
		AppPath:    filepath.Join(src, "build", "bin", "Aseprite.app"),
		Executable: filepath.Join(src, "build", "bin", "Aseprite.app", "Contents", "MacOS", "aseprite"),
		DataDir:    filepath.Join(src, "build", "bin", "Aseprite.app", "Contents", "Resources", "data"),
	}
	// The fake executable reports 1.3.18.5 while we expect 1.3.18.6.
	r := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if len(c.Args) == 1 && c.Args[0] == "--version" && strings.HasSuffix(c.Name, "aseprite") {
			return runner.Result{Stdout: "Aseprite v1.3.18.5\n"}, true
		}
		return runner.Result{}, false
	}}
	if err := VerifyBuild(context.Background(), r, out, "1.3.18.6"); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Errorf("err = %v", err)
	}
}

func TestVerifyBuildMissingData(t *testing.T) {
	src := fixtureSource(t)
	app := filepath.Join(src, "build", "bin", "Aseprite.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "aseprite"), []byte("mach-o"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := BuildOutput{
		AppPath:    app,
		Executable: filepath.Join(app, "Contents", "MacOS", "aseprite"),
		DataDir:    filepath.Join(app, "Contents", "Resources", "data"),
	}
	if err := VerifyBuild(context.Background(), &runner.FakeRunner{}, out, "1.3.18.6"); err == nil {
		t.Error("empty data dir must fail verification")
	}
}

func TestVerifyBuildMissingExecutable(t *testing.T) {
	src := fixtureSource(t)
	app := filepath.Join(src, "build", "bin", "Aseprite.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "Resources", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Resources", "data", "d"), []byte("d"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := BuildOutput{AppPath: app, Executable: filepath.Join(app, "Contents", "MacOS", "aseprite")}
	if err := VerifyBuild(context.Background(), &runner.FakeRunner{}, out, "1.3.18.6"); err == nil {
		t.Error("missing executable must fail verification")
	}
}

func TestVerifyInstalled(t *testing.T) {
	src := fixtureSource(t)
	fakeBuildOutput(t, src, "1.3.18.6")
	app := filepath.Join(src, "build", "bin", "Aseprite.app")
	r := &runner.FakeRunner{Match: anyVersionFaker()}
	if err := VerifyInstalled(context.Background(), r, app, "1.3.18.6"); err != nil {
		t.Errorf("VerifyInstalled: %v", err)
	}
	if err := VerifyInstalled(context.Background(), r, app, "2.0"); err == nil {
		t.Error("version mismatch must fail for installed bundles")
	}
}

func TestTail(t *testing.T) {
	s := "l1\nl2\nl3\nl4\nl5"
	if got := tail(s, 2); got != "l4\nl5" {
		t.Errorf("tail = %q", got)
	}
	if got := tail(s, 10); got != "l1\nl2\nl3\nl4\nl5" {
		t.Errorf("tail = %q", got)
	}
}

func TestUpstreamBuildErrorMessage(t *testing.T) {
	e := &UpstreamBuildError{ExitCode: 1}
	if !strings.Contains(e.Error(), "code 1") {
		t.Errorf("message = %q", e.Error())
	}
}

// anyVersionFaker answers --version for the fixture's fake executable
// with a plausible Aseprite version banner.
func anyVersionFaker() func(runner.Command) (runner.Result, bool) {
	return func(c runner.Command) (runner.Result, bool) {
		if len(c.Args) == 1 && c.Args[0] == "--version" && strings.HasSuffix(c.Name, "aseprite") {
			return runner.Result{Stdout: "Aseprite v1.3.18.6\n"}, true
		}
		return runner.Result{}, false
	}
}

var _ = time.Second
