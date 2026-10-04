package aseprite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// Windows build contract facts, derived from upstream's own scripts:
// build.sh's unconditionally-assigned user build type, its cmake
// invocation ("cmake --build <dir> -- aseprite"), and the executable
// name it reports for Windows ("bin/aseprite$exe" with exe=.exe).
// These mirror what upstream runs for ordinary users; like the macOS
// bundle rule, they are contract facts, not a version matrix.
const (
	windowsBuildType   = "RelWithDebInfo"
	windowsCmakeTarget = "aseprite"
	windowsExeName     = "aseprite.exe"
)

// buildWindows drives the documented upstream Windows flow directly:
// the same two commands upstream's build.sh executes, run under a
// Visual Studio toolchain environment supplied by the platform. bash
// is deliberately not involved (spec §9: no Git Bash requirement).
//
// Upstream steps being mirrored (see build.sh, user mode):
//
//  1. cmake -B <src>/build -S <src> -G Ninja
//     -DCMAKE_BUILD_TYPE=RelWithDebInfo -DLAF_BACKEND=skia
//     -DSKIA_DIR=<skia> -DSKIA_LIBRARY_DIR=<skia>/out/Release-<cpu>
//  2. cmake --build <src>/build -- aseprite
//
// Skia is passed explicitly on the command line (that is what
// upstream's cmake configure consumes), so the .build config seeding
// and the .deps symlink of the bash path are unnecessary here — and
// the symlink would need Windows symlink privileges anyway.
func (b Builder) buildWindows(ctx context.Context, req BuildRequest) error {
	if req.Contract.OS != "windows" {
		return fmt.Errorf("buildWindows invoked for contract OS %q", req.Contract.OS)
	}

	buildDir := filepath.Join(req.SourceDir, "build")
	baseEnv := buildEnvWith(req.ExtraBinDirs, req.Env)

	// Step 1: configure with the Ninja generator and upstream's Skia
	// variables — the exact flag set build.sh composes for users.
	configureCmd := runner.Command{
		Name: "cmake",
		Args: []string{
			"-B", buildDir,
			"-S", req.SourceDir,
			"-G", "Ninja",
			"-DCMAKE_BUILD_TYPE=" + windowsBuildType,
			"-DLAF_BACKEND=skia",
			"-DSKIA_DIR=" + req.SkiaDir,
			"-DSKIA_LIBRARY_DIR=" + filepath.Join(req.SkiaDir, "out", "Release-"+req.CPU),
		},
		Dir: req.SourceDir,
		Env: baseEnv,
	}
	b.logCommand(configureCmd)
	// Everything before ninja reports counters is configuration from
	// the user's point of view.
	b.announceConfigure()
	configureRes, err := b.buildRunner().Run(ctx, configureCmd)
	b.logCommandResult("cmake configure", configureRes)
	if err := requireOK(configureRes, err); err != nil {
		return err
	}

	// Step 2: build the aseprite target.
	buildCmd := runner.Command{
		Name: "cmake",
		Args: []string{"--build", buildDir, "--", windowsCmakeTarget},
		Dir:  req.SourceDir,
		Env:  baseEnv,
	}
	b.logCommand(buildCmd)
	buildRes, err := b.buildRunner().Run(ctx, buildCmd)
	b.logCommandResult("cmake build", buildRes)
	return requireOK(buildRes, err)
}

// buildEnvWith merges explicit environment overrides (the captured
// Visual Studio environment) with the cached-tools PATH prepend into a
// single coherent environment: exactly one PATH entry, cached tool
// dirs first, MSVC PATH preserved behind them (first-match wins in
// PATH lookups, so the tools build commands use are the resolved
// ones). Key matching for PATH is case-insensitive because vcvars's
// `set` output reports Windows' canonical "Path".
func buildEnvWith(extraBinDirs []string, env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if key := envKey(e); strings.EqualFold(key, "PATH") {
			continue // merged below; never emitted twice
		}
		out = append(out, e)
	}
	return append(out, buildEnvOn(extraBinDirs, vcvarsPATH(env)))
}

// vcvarsPATH extracts the PATH value from the explicit env, if any.
func vcvarsPATH(env []string) string {
	for _, e := range env {
		if key := envKey(e); strings.EqualFold(key, "PATH") {
			return strings.TrimPrefix(e, key+"=")
		}
	}
	return ""
}

// buildEnvOn recomposes PATH: ExtraBinDirs first, then base (the
// captured environment's PATH when present, the process PATH on plain
// platforms).
func buildEnvOn(extraBinDirs []string, base string) string {
	if base == "" {
		base = os.Getenv("PATH")
	}
	parts := append([]string(nil), extraBinDirs...)
	parts = append(parts, base)
	return "PATH=" + strings.Join(parts, string(filepath.ListSeparator))
}

func envKey(e string) string {
	if i := strings.IndexByte(e, '='); i >= 0 {
		return e[:i]
	}
	return e
}
