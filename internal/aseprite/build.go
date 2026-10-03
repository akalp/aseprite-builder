package aseprite

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aseprite-builder/aseprite-builder/internal/runner"
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
}

// BuildOutput is the produced macOS artifact.
type BuildOutput struct {
	// AppPath is the produced Aseprite.app bundle.
	AppPath string
	// Executable is the bundle's Mach-O executable.
	Executable string
	// DataDir is the bundle's runtime data directory.
	DataDir string
	// BuildDir is the directory the upstream build wrote into.
	BuildDir string
	// IconsDir is the upstream icon set directory (data/icons) used by
	// the macOS installer to generate .icns bundle icons. Empty when
	// the source tree does not ship one.
	IconsDir string
}

// Builder orchestrates the upstream build flow. It does not reimplement
// Aseprite's build logic; it prepares the environment upstream's
// build.sh expects (its own config files, Skia location, tool PATH) and
// invokes it.
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
//   - writes the bundle to <source>/build/bin/Aseprite.app.
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

// Build runs the upstream build flow and returns the produced artifact.
func (b Builder) Build(ctx context.Context, req BuildRequest) (BuildOutput, error) {
	if err := b.seedUpstreamConfig(req); err != nil {
		return BuildOutput{}, err
	}

	env := buildEnv(req.ExtraBinDirs)
	cmd := runner.Command{
		Name: "bash",
		Args: []string{req.Contract.BuildScript, "--auto", "--norun"},
		Dir:  req.SourceDir,
		Env:  env,
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

	if b.Progress != nil {
		// Announce the configure phase immediately: everything the
		// upstream script does before ninja reports counters is
		// configuration from the user's point of view.
		b.Progress(BuildProgress{Phase: BuildPhaseConfigure})
	}
	res, err := b.runnerFor().Run(ctx, cmd)
	if err != nil {
		// Context cancellation and start failures.
		return BuildOutput{}, fmt.Errorf("upstream build failed: %w", err)
	}
	if b.Log != nil {
		b.Log.CommandResult("build.sh", res.ExitCode, res.Stdout, res.Stderr, res.Duration)
	}
	if !res.OK() {
		return BuildOutput{}, &UpstreamBuildError{
			ExitCode: res.ExitCode,
			Tail:     tail(res.Combined(), 64),
		}
	}

	out, err := locateBuildOutput(req)
	if err != nil {
		return BuildOutput{}, err
	}
	return out, nil
}

// runnerFor constructs the actual Runner for the build invocation.
// Injected Runner wins (tests); otherwise a concrete runner streams
// output and feeds the progress tracker simultaneously.
func (b Builder) runnerFor() runner.Runner {
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
		// above no longer matches a future build.sh.
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
// build-selection prompt: "1" when its enumeration loop finds existing
// builds (build.sh lists <builds_dir/*/CMakeCache.txt) and its first
// numbered menu entry is the user-mode build directory. Nothing is sent
// when no prior build exists (first build never prompts).
func buildStdinFor(sourceDir string, _ Contract) []byte {
	matches, err := filepath.Glob(filepath.Join(sourceDir, "build", "**", "CMakeCache.txt"))
	if err != nil || len(matches) == 0 {
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

// locateBuildOutput finds the produced macOS application bundle at the
// path the upstream contract defines.
func locateBuildOutput(req BuildRequest) (BuildOutput, error) {
	binDir := filepath.Join(req.SourceDir, "build", "bin")
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
