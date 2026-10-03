// Package dependencies resolves runtime build tools (CMake, Ninja,
// Skia) at build time.
//
// Resolution order, per the runtime-resolved dependency model:
//
//  1. an existing compatible system tool (preferred),
//  2. a compatible tool previously downloaded into the user-local cache,
//  3. the latest compatible artifact from the tool's authoritative
//     upstream source, downloaded into the cache (never installed
//     globally, never bundled with this tool).
//
// The package carries no version matrix: system/cache candidates are
// checked against a minimum discovered from the Aseprite source at
// runtime, and downloads always take the current upstream release.
package dependencies

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/version"
)

// Tool is one resolved build tool.
type Tool struct {
	// Name is the canonical tool name ("cmake", "ninja").
	Name string
	// Version is the parsed version of this specific tool.
	Version version.Version
	// Path is the absolute path of the executable.
	Path string
	// BinDir is the directory containing the executable, suitable for
	// prepending to PATH. It equals filepath.Dir(Path) except where a
	// tool ships nested bin directories (CMake bundles).
	BinDir string
	// Source is "system" or "cache".
	Source string
}

// String renders the tool for logs and progress lines.
func (t Tool) String() string {
	return fmt.Sprintf("%s %s (%s)", t.Name, t.Version, t.Source)
}

// probeArgs maps tool names to the arguments that reliably print a
// version. Only plain invocation is used — no version-specific hacks.
var probeArgs = map[string][]string{
	"cmake": {"--version"},
	"ninja": {"--version"},
}

// versionExtractors parse the probe output per tool.
var versionExtractors = map[string]func(string) (version.Version, error){
	"cmake": parseCMakeVersion,
	"ninja": parseNinjaVersion,
}

func parseCMakeVersion(out string) (version.Version, error) {
	// "cmake version 4.1.2\n\nCMake suite maintained..."
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "cmake version "); ok {
			return version.Parse(strings.TrimSpace(v))
		}
	}
	return version.Version{}, fmt.Errorf("cmake --version output %q has no version", out)
}

func parseNinjaVersion(out string) (version.Version, error) {
	// "1.13.1"
	return version.Parse(strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]))
}

// DetectSystemTask returns a system Tool of name by invoking its
// --version probe. The second return value distinguishes "not found"
// from real failures.
func DetectSystemTool(ctx context.Context, r runner.Runner, name string) (Tool, bool, error) {
	args, ok := probeArgs[name]
	if !ok {
		return Tool{}, false, fmt.Errorf("no probe configured for tool %q", name)
	}
	extract := versionExtractors[name]

	res, err := r.Run(ctx, runner.Command{Name: name, Args: args})
	if err != nil {
		// Executable not on PATH (exec returns an error) or context
		// expired: treat as not-found unless the context itself failed.
		if ctx.Err() != nil {
			return Tool{}, false, ctx.Err()
		}
		return Tool{}, false, nil
	}
	if !res.OK() {
		return Tool{}, false, fmt.Errorf("%s exited with code %d: %s", name, res.ExitCode, tailLine(res.Stderr))
	}
	v, err := extract(res.Stdout)
	if err != nil {
		return Tool{}, false, fmt.Errorf("parse %s version: %w", name, err)
	}
	return Tool{
		Name:    name,
		Version: v,
		Path:    name, // resolved on PATH at invocation time
		BinDir:  "",   // system PATH needs no prepend
		Source:  "system",
	}, true, nil
}

// ProbeCachedTool runs a cached tool binary to confirm it is usable on
// this machine (architecture matches, not corrupted) and parses its
// version.
func ProbeCachedTool(ctx context.Context, r runner.Runner, name, path string) (version.Version, error) {
	args, ok := probeArgs[name]
	if !ok {
		return version.Version{}, fmt.Errorf("no probe configured for tool %q", name)
	}
	extract := versionExtractors[name]

	res, err := r.Run(ctx, runner.Command{Name: path, Args: args})
	if err != nil {
		return version.Version{}, fmt.Errorf("run cached %s at %s: %w", name, path, err)
	}
	if !res.OK() {
		return version.Version{}, fmt.Errorf("cached %s at %s exited with code %d", name, path, res.ExitCode)
	}
	return extract(res.Stdout)
}

// ToolBinaryLocators map a tool name to the relative locations its
// distribution layouts use inside an extracted artifact. CMake's macOS
// distribution nests the binary inside CMake.app; Ninja ships it at the
// extraction root.
var toolBinaryLocators = map[string][]string{
	"cmake": {filepath.Join("CMake.app", "Contents", "bin", "cmake")},
	"ninja": {"ninja"},
}

// LocateToolBinary searches an extracted tool distribution for the
// tool's executable, preferring the known locator paths. Returns ""
// when nothing recognizable is found — callers must fail rather than
// guess.
func LocateToolBinary(root, name string) string {
	for _, rel := range toolBinaryLocators[name] {
		candidate := filepath.Join(root, rel)
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	// Fall back to a shallow search for tool distributions whose layout
	// changed shape but still contain exactly one unambiguous binary.
	return shallowLocate(root, name)
}

var cmakeVersionAssetRe = regexp.MustCompile(`^cmake-(\d+\.\d+\.\d+)-macos-universal\.tar\.gz$`)
var ninjaVersionTagRe = regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)

func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// shallowLocate performs a bounded search (max depth 4) for a single
// executable named name. Ambiguity (multiple matches) is reported as
// not found.
func shallowLocate(root, name string) string {
	var found []string
	depth := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			depth++
			if depth > 5 {
				return fs.SkipAll
			}
			return nil
		}
		if filepath.Base(path) == name && isExecutableFile(path) {
			found = append(found, path)
		}
		return nil
	})
	if len(found) == 1 {
		return found[0]
	}
	return ""
}

func tailLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}
