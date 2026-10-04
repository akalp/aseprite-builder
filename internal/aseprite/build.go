package aseprite

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// BuildRequest is everything the builder needs to run the upstream
// build for one Aseprite version.
type BuildRequest struct {
	// SourceDir is the extracted official source tree.
	SourceDir string
	// Contract is the inspected upstream contract for SourceDir.
	Contract Contract
	// SkiaDir is the resolved, verified Skia artifact directory (the
	// directory whose layout matches upstream expectations: contains
	// out/Release-<cpu> and third_party/externals/...).
	SkiaDir string
	// CPU follows laf/misc/platform.sh naming ("arm64", "x64").
	CPU string
	// ExtraBinDirs are directories containing resolved runtime tools
	// (from the builder's cache) to prepend to PATH. System tools need
	// no entry: they are already reachable through PATH.
	ExtraBinDirs []string
	// Env carries explicit environment overrides for the build
	// (KEY=value), such as the Visual Studio environment captured from
	// vcvars64.bat on Windows. Empty outside platforms that need it.
	Env []string
}

// BuildOutput is the produced artifact.
type BuildOutput struct {
	// AppPath is the produced Aseprite.app bundle (macOS only).
	AppPath string
	// Executable is the produced executable. On macOS it is the
	// bundle's Mach-O executable; on Windows the plain aseprite.exe.
	Executable string
	// BinDir is the directory holding the executable plus everything
	// needed to run it (the data dir, DLLs). It is the payload the
	// non-bundle platform installers activate.
	BinDir string
	// DataDir is the runtime data directory.
	DataDir string
	// BuildDir is the directory the upstream build wrote into.
	BuildDir string
	// IconsDir is the upstream icon set directory (data/icons) used by
	// the macOS installer to generate .icns bundle icons. Empty when
	// the source tree does not ship one.
	IconsDir string
}

// Builder orchestrates the upstream build flow. It does not reimplement
// Aseprite's build logic; it prepares the environment and invokes the
// upstream flow for the recognized contract platform:
//
//   - macOS/Linux: runs upstream's build.sh --auto (its own config
//     files, Skia location, tool PATH prepared), which configures
//     cmake and builds into <source>/build/bin/Aseprite.app.
//   - Windows: runs the two cmake commands build.sh itself runs, under
//     a Visual Studio environment the platform supplies.
type Builder struct {
	// Runner, when set, is used directly (tests inject fakes here).
	Runner runner.Runner
	// Stream receives the raw upstream build output for logging. When
	// Runner is nil (the production path) a concrete runner is created
	// with Stream and progress reporting wired together.
	Stream io.Writer
	// Progress reports compile/configure sub-state derived from the
	// upstream build output.
	Progress BuildProgressSink
	// Log receives per-command records; nil disables.
	Log CommandLogger
}

// CommandLogger records process activity. logging.Logger implements it.
type CommandLogger interface {
	Command(name string, args []string, env []string, dir string)
	CommandResult(name string, exitCode int, stdout, stderr string, duration time.Duration)
} // buildConfigNames are the config files build.sh reads under
// <source>/.build/. The builder pre-writes them so the upstream script
// runs non-interactively in its documented "--auto" user mode.
//
//	userkind     → "user" (skip user/developer prompt)
//	builds_dir   → the source dir (user default: build into ./build)
//	*_skia_dir   → the resolved Skia dir; the file name depends on the
//	               git branch detection upstream does, which for a
//	               Source.zip (no .git) resolves to "_skia_dir"; the
//	               main/beta names cover a future upstream that
//	               detects the branch differently.
var buildConfigNames = []string{"_skia_dir", "main_skia_dir", "beta_skia_dir"}

// Build runs the upstream build flow and returns the produced
// artifact.
//
// The flow is per-contract-platform: macOS/Linux orchestrate upstream's
// own build.sh (contract-faithful: bash is guaranteed there), while
// Windows drives the documented upstream contract directly — the two
// cmake commands build.sh itself runs — inside a Visual Studio
// environment the platform provides. Both paths feed the same logging,
// progress reporting and output verification below.
func (b Builder) Build(ctx context.Context, req BuildRequest) (BuildOutput, error) {
	var err error
	switch req.Contract.OS {
	case "windows":
		err = b.buildWindows(ctx, req)
	default:
		err = b.buildUnix(ctx, req)
	}
	if err != nil {
		return BuildOutput{}, err
	}
	return locateBuildOutput(req)
}

// buildUnix orchestrates upstream's own build.sh (macOS/Linux).
//
// The expected upstream invocation contract (verified at build time):
//
//	bash build.sh --auto --norun
//
// in --auto mode upstream:
//   - validates submodules of the extracted source,
//   - reads laf/misc/skia-tag.txt and reuses an existing Skia dir
//     (or downloads the official prebuilt package itself),
//   - configures cmake with the Ninja generator,
//   - runs cmake --build ... -- aseprite,
//   - writes the bundle to <source>/build/bin/Aseprite.app (macOS).
func (b Builder) buildUnix(ctx context.Context, req BuildRequest) error {
	if err := b.seedUpstreamConfig(req); err != nil {
		return err
	}

	cmd := runner.Command{
		Name: "bash",
		Args: []string{req.Contract.BuildScript, "--auto", "--norun"},
		Dir:  req.SourceDir,
		Env:  buildEnv(req.ExtraBinDirs),
		// Upstream build.sh asks for a build selection when its own
		// state directory already lists builds (e.g. a prior install
		// attempt left <source>/build with a CMakeCache). Our workdirs
		// only ever contain the single user-mode "build" directory, so
		// answering "1" selects the existing build; on a first build
		// stdin stays empty and --auto skips prompts entirely.
		Stdin: buildStdinFor(req.SourceDir, req.Contract),
	}
	if b.Log != nil {
		b.Log.Command(cmd.Name, cmd.Args, cmd.Env, cmd.Dir)
	}

	// Announce the configure phase immediately: everything the
	// upstream script does before ninja reports counters is
	// configuration from the user's point of view.
	b.announceConfigure()
	res, err := b.buildRunner().Run(ctx, cmd)
	b.logCommandResult("build.sh", res)
	return requireOK(res, err)
}

// buildRunner constructs the runner for one build invocation.
// Injected Runner wins (tests); otherwise a concrete runner streams
// output and feeds the progress tracker simultaneously.
func (b Builder) buildRunner() runner.Runner {
	if b.Runner != nil {
		return b.Runner
	}
	var stream io.Writer = io.Discard
	if b.Stream != nil {
		stream = b.Stream
	}
	if b.Progress == nil {
		return runner.ConcreteRunner{Stream: stream}
	}
	return runner.ConcreteRunner{Stream: io.MultiWriter(stream, newProgressTracker(b.Progress))}
}

// logCommand records a command invocation when a logger is wired.
func (b Builder) logCommand(cmd runner.Command) {
	if b.Log != nil {
		b.Log.Command(cmd.Name, cmd.Args, cmd.Env, cmd.Dir)
	}
}

// logCommandResult records a command outcome when a logger is wired.
func (b Builder) logCommandResult(name string, res runner.Result) {
	if b.Log != nil {
		b.Log.CommandResult(name, res.ExitCode, res.Stdout, res.Stderr, res.Duration)
	}
}

// announceConfigure emits the configure-phase progress tick: everything
// the upstream flow does before ninja reports counters is
// configuration from the user's point of view.
func (b Builder) announceConfigure() {
	if b.Progress != nil {
		b.Progress(BuildProgress{Phase: BuildPhaseConfigure})
	}
}

// requireOK turns a failed step into the typed upstream build error.
func requireOK(res runner.Result, err error) error {
	if err != nil {
		// Context cancellation and start failures.
		return fmt.Errorf("upstream build failed: %w", err)
	}
	if res.OK() {
		return nil
	}
	return &UpstreamBuildError{
		ExitCode: res.ExitCode,
		Tail:     tail(res.Combined(), 64),
	}
}

// UpstreamBuildError wraps a non-zero exit from the upstream build with
// a log tail for user-facing reporting. The full output lives in the
// operation log, never as the primary error message.
type UpstreamBuildError struct {
	ExitCode int
	Tail     string
}

func (e *UpstreamBuildError) Error() string {
	return fmt.Sprintf("upstream build exited with code %d", e.ExitCode)
}

// seedUpstreamConfig prepares the files build.sh reads from
// <source>/.build/ and links the resolved Skia into the location
// upstream looks at first.
func (b Builder) seedUpstreamConfig(req BuildRequest) error {
	configDir := filepath.Join(req.SourceDir, ".build")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create upstream build config dir: %w", err)
	}
	for name, content := range map[string]string{
		"userkind":   "user",
		"builds_dir": req.SourceDir,
	} {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(content+"\n"), 0o644); err != nil {
			return fmt.Errorf("write upstream build config %s: %w", name, err)
		}
	}
	if req.SkiaDir != "" {
		absSkia, err := filepath.Abs(req.SkiaDir)
		if err != nil {
			return fmt.Errorf("resolve Skia dir: %w", err)
		}
		for _, name := range buildConfigNames {
			if err := os.WriteFile(filepath.Join(configDir, name), []byte(absSkia+"\n"), 0o644); err != nil {
				return fmt.Errorf("write upstream Skia config %s: %w", name, err)
			}
		}
		// Additionally seed the default .deps location upstream checks,
		// so the resolved Skia is used even when the config-file naming
		// above no longer matches a future build.sh. A contract without
		// a Skia dir name cannot name the link (inspection always
		// produces one; this guards hand-built requests).
		if req.Contract.SkiaDirName == "" {
			return fmt.Errorf("contract is missing the Skia dir name; cannot seed the upstream Skia location")
		}
		depsDir := filepath.Join(req.SourceDir, ".deps")
		link := filepath.Join(depsDir, req.Contract.SkiaDirName)
		if err := os.MkdirAll(depsDir, 0o755); err != nil {
			return fmt.Errorf("create source deps dir: %w", err)
		}
		if err := ensureSymlink(link, absSkia); err != nil {
			return fmt.Errorf("link cached Skia into source tree: %w", err)
		}
	}
	return nil
}

// buildStdinFor composes the stdin answer for upstream build.sh's
// build-selection prompt: "1" when its enumeration loop would list the
// user-mode build. Upstream enumerates "$builds_dir"/*/CMakeCache.txt"
// with builds_dir=source, matching build/CMakeCache.txt (the user-mode
// in-source build) plus any nested build/*/CMakeCache.txt artifacts;
// either shape means the menu will prompt, and entry 1 is our build.
func buildStdinFor(sourceDir string, _ Contract) []byte {
	direct, err := filepath.Glob(filepath.Join(sourceDir, "build", "CMakeCache.txt"))
	if err != nil {
		return nil
	}
	nested, err := filepath.Glob(filepath.Join(sourceDir, "build", "*", "CMakeCache.txt"))
	if err != nil {
		return nil
	}
	if len(direct) == 0 && len(nested) == 0 {
		return nil
	}
	return []byte("1\n")
}

// buildEnv composes the environment for the upstream build: PATH is
// prepended with cached tool bin dirs; nothing else is overridden.
func buildEnv(extraBinDirs []string) []string {
	inherited := os.Getenv("PATH")
	prefix := strings.Join(append(append([]string(nil), extraBinDirs...), ""), string(filepath.ListSeparator))
	if inherited != "" {
		prefix += string(filepath.ListSeparator) + inherited
	}
	return []string{"PATH=" + prefix}
}

// locateBuildOutput finds the artifact at the path the recognized
// contract defines. On macOS it is the application bundle; on Windows
// the plain executable plus the data directory next to it.
func locateBuildOutput(req BuildRequest) (BuildOutput, error) {
	binDir := filepath.Join(req.SourceDir, "build", "bin")
	switch req.Contract.OS {
	case "windows":
		return locateWindowsBuildOutput(req, binDir)
	default:
		return locateAppBundleOutput(req, binDir)
	}
}

// locateWindowsBuildOutput validates the Windows exe + bin/data
// output contract.
func locateWindowsBuildOutput(req BuildRequest, binDir string) (BuildOutput, error) {
	violation := func(reason string) (BuildOutput, error) {
		return BuildOutput{}, &ContractViolationError{Stage: "build output", Reason: reason}
	}
	exe := filepath.Join(binDir, windowsExeName)
	fi, err := os.Stat(exe)
	if err != nil || fi.IsDir() {
		return violation(fmt.Sprintf(
			"the upstream build did not produce %s (the Windows output contract this builder relies on)", exe))
	}
	data := filepath.Join(binDir, "data")
	if runtimeDataEmpty(data) {
		return violation(fmt.Sprintf(
			"the built data directory %s is empty; the upstream data copy step did not run", data))
	}
	return BuildOutput{
		AppPath:    "", // no bundle on Windows
		Executable: exe,
		BinDir:     binDir,
		DataDir:    data,
		BuildDir:   filepath.Join(req.SourceDir, "build"),
		IconsDir:   sourceIconsDir(req.SourceDir),
	}, nil
}

// locateAppBundleOutput validates the macOS bundle output contract.
func locateAppBundleOutput(req BuildRequest, binDir string) (BuildOutput, error) {
	app := filepath.Join(binDir, "Aseprite.app")
	if fi, err := os.Stat(app); err != nil || !fi.IsDir() {
		return BuildOutput{}, &ContractViolationError{
			Stage: "build output",
			Reason: fmt.Sprintf(
				"the upstream build did not produce %s (the contract this builder relies on)", app),
		}
	}
	executable := filepath.Join(app, "Contents", "MacOS", "aseprite")
	if fi, err := os.Stat(executable); err != nil || fi.IsDir() {
		return BuildOutput{}, &ContractViolationError{
			Stage: "build output",
			Reason: fmt.Sprintf(
				"the built bundle does not contain the expected executable at %s", executable),
		}
	}
	return BuildOutput{
		AppPath:    app,
		Executable: executable,
		BinDir:     binDir,
		DataDir:    filepath.Join(app, "Contents", "Resources", "data"),
		BuildDir:   filepath.Join(req.SourceDir, "build"),
		IconsDir:   sourceIconsDir(req.SourceDir),
	}, nil
}

// sourceIconsDir returns the upstream icon set directory when present.
// The official source ships data/icons with size-named PNG members
// (ase16.png … ase256.png, plus hd/asehd.png), which macOS uses to
// generate .icns bundle icons.
func sourceIconsDir(sourceDir string) string {
	icons := filepath.Join(sourceDir, "data", "icons")
	if fi, err := os.Stat(icons); err == nil && fi.IsDir() {
		return icons
	}
	return ""
}

// ContractViolationError reports a mismatch between what the builder
// understands about the upstream build and what actually happened.
type ContractViolationError struct {
	Stage  string
	Reason string
}

func (e *ContractViolationError) Error() string {
	return fmt.Sprintf("upstream build contract violation at %s: %s", e.Stage, e.Reason)
}

// ensureSymlink points link at target, creating or updating as needed.
// A non-symlink occupant at link (leftover directory or file) is
// reported explicitly rather than failing with a cryptic "file exists".
func ensureSymlink(link, target string) error {
	if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink != 0 {
		existing, readErr := os.Readlink(link)
		if readErr == nil && existing == target {
			return nil // already correct
		}
		// Different target: replace it.
	} else if err == nil {
		return fmt.Errorf(
			"cannot create symlink %s: a non-symlink %s occupies the path",
			link, kindOf(info))
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot replace symlink %s: %w", link, err)
	}
	return os.Symlink(target, link)
}

// kindOf renders a filesystem entry's kind for diagnostics.
func kindOf(info os.FileInfo) string {
	switch {
	case info.IsDir():
		return "directory"
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	default:
		return "file"
	}
}

// tail returns the last n lines of s for error reporting.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
