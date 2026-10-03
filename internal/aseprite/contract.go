package aseprite

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akalp/aseprite-builder/internal/version"
)

// contractLevel identifies the shape of the upstream build contract
// this builder understands. It is recorded in the install state so a
// future builder can tell which contract an installation was built
// under.
//
// 1 = build.sh orchestrator + skia-url.sh Skia resolution + Aseprite.app
//
//	output bundle on macOS.
const contractLevel = 1

// Contract summarizes the upstream build contract discovered by
// inspecting an extracted Aseprite source tree. Every value is derived
// from the tree itself; nothing here is hardcoded per-release.
type Contract struct {
	// CMakeMinVersion is parsed from the source's top-level
	// CMakeLists.txt (e.g. "3.20").
	CMakeMinVersion version.Version
	// SkiaTag is the upstream-required Skia revision, read from
	// laf/misc/skia-tag.txt (e.g. "m124-08a5439a6b").
	SkiaTag string
	// SkiaDirName mirrors build.sh's derived cache dir name
	// ("skia-<first-field-of-tag>").
	SkiaDirName string
	// BundleOutput is the relative path, inside the build directory,
	// where upstream's macOS build produces the application bundle.
	// Derived from src/CMakeLists.txt's data-output contract.
	BundleOutput string
	// BuildScript is the absolute path of the upstream entry point.
	BuildScript string
	// Level is the builder-side contract level recognized here.
	Level int
}

// UnknownContractError reports a source tree this builder cannot
// safely orchestrate.
type UnknownContractError struct {
	Reasons []string
}

func (e UnknownContractError) Error() string {
	return "unrecognized Aseprite build contract: " + strings.Join(e.Reasons, "; ")
}

// Inspector validates the build contract of an extracted Aseprite
// source tree before any build happens.
type Inspector struct{}

// NewInspector returns an Inspector.
func NewInspector() Inspector { return Inspector{} }

var (
	cmakeMinVersionRe = regexp.MustCompile(`(?m)^\s*cmake_minimum_required\s*\(\s*VERSION\s+([0-9.]+)`)
	projectNameRe     = regexp.MustCompile(`(?m)^\s*project\s*\(\s*aseprite\b`)
	bundleOutputRe    = regexp.MustCompile(`bin/Aseprite\.app`)
)

// requiredFiles are structural markers from the upstream tree. The
// Aseprite build system itself demands these (see the top-level
// CMakeLists.txt "Check repository status" block and build.sh's own
// checks); their presence identifies a complete, orchestratable tree.
var requiredFiles = []string{
	"EULA.txt",
	".gitmodules",
	"build.sh",
	"CMakeLists.txt",
	"laf/CMakeLists.txt",
	"laf/clip/CMakeLists.txt",
	"laf/misc/platform.sh",
	"laf/misc/skia-tag.txt",
	"laf/misc/skia-url.sh",
}

// darwinRequiredFiles are additional markers needed for the macOS
// application bundle output contract.
var darwinRequiredFiles = []string{
	"src/main/osx/Info.plist",
}

// Inspect validates the source tree and derives the build contract.
//
// Failing inspection is always safe: nothing has been built or
// installed yet.
func (Inspector) Inspect(sourceDir string, wantsAppBundle bool) (Contract, error) {
	var reasons []string

	// Incomplete-source check: mirror upstream CMakeLists.txt logic —
	// laf/CMakeLists.txt and laf/clip/CMakeLists.txt must exist. Their
	// absence is exactly what upstream's fatal error describes for a
	// truncated source download.
	for _, f := range requiredFiles {
		if wantsSourceFile(sourceDir, f) {
			continue
		}
		reasons = append(reasons, fmt.Sprintf("missing %s (incomplete source archive?)", f))
	}
	if wantsAppBundle {
		for _, f := range darwinRequiredFiles {
			if !wantsSourceFile(sourceDir, f) {
				reasons = append(reasons, fmt.Sprintf("missing %s", f))
			}
		}
	}
	if len(reasons) > 0 {
		return Contract{}, UnknownContractError{Reasons: reasons}
	}

	contract := Contract{
		BuildScript: filepath.Join(sourceDir, "build.sh"),
		Level:       contractLevel,
	}

	// Discover the minimum CMake version from the source itself.
	top, err := os.ReadFile(filepath.Join(sourceDir, "CMakeLists.txt")) //nolint:gosec // path from trusted app logic
	if err != nil {
		return Contract{}, UnknownContractError{Reasons: []string{"cannot read CMakeLists.txt: " + err.Error()}}
	}
	if m := cmakeMinVersionRe.FindSubmatch(top); m != nil {
		if v, err := version.Parse(string(m[1])); err == nil {
			contract.CMakeMinVersion = v
		}
	}
	if contract.CMakeMinVersion.IsZero() {
		return Contract{}, UnknownContractError{Reasons: []string{
			"cannot determine the required CMake version from CMakeLists.txt",
		}}
	}
	if m := projectNameRe.FindSubmatch(top); m == nil {
		return Contract{}, UnknownContractError{Reasons: []string{
			"CMakeLists.txt does not declare project(aseprite ...)",
		}}
	}

	// On macOS, verify the bundle output contract exists in the
	// upstream build rules.
	if wantsAppBundle {
		srcCmake, err := os.ReadFile(filepath.Join(sourceDir, "src", "CMakeLists.txt")) //nolint:gosec // path from trusted app logic
		if err != nil {
			return Contract{}, UnknownContractError{Reasons: []string{
				"cannot read src/CMakeLists.txt: " + err.Error(),
			}}
		}
		if !bundleOutputRe.Match(srcCmake) {
			return Contract{}, UnknownContractError{Reasons: []string{
				"src/CMakeLists.txt no longer describes the Aseprite.app output contract",
			}}
		}
		contract.BundleOutput = filepath.Join("build", "bin", "Aseprite.app")
	}

	// Discover the Skia requirement.
	tag, err := discoverSkiaTag(sourceDir)
	if err != nil {
		return Contract{}, UnknownContractError{Reasons: []string{err.Error()}}
	}
	contract.SkiaTag = tag
	contract.SkiaDirName = skiaDirNameFromTag(tag)

	return contract, nil
}

// discoverSkiaTag reads the upstream-required Skia revision from
// laf/misc/skia-tag.txt. This file is the upstream source of truth for
// which Skia revision the current source expects; the builder never
// maintains its own version→Skia mapping.
func discoverSkiaTag(sourceDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(sourceDir, "laf", "misc", "skia-tag.txt")) //nolint:gosec // path from trusted app logic
	if err != nil {
		return "", fmt.Errorf("cannot read Skia requirement: %w", err)
	}
	tag := strings.TrimSpace(string(data))
	if tag == "" {
		return "", fmt.Errorf("Skia requirement file laf/misc/skia-tag.txt is empty")
	}
	if strings.ContainsAny(tag, " \t\n;|&$`") {
		return "", fmt.Errorf("Skia requirement %q contains unexpected characters", tag)
	}
	return tag, nil
}

// skiaDirNameFromTag mirrors upstream build.sh's derivation:
//
//	possible_skia_dir_name = skia-$(echo $skia_tag | cut -d "-" -f 1)
//
// If this derivation no longer matches upstream, the consequence is
// only that the builder's cache-seeded directory is not picked up and
// build.sh falls back to its own resolution.
func skiaDirNameFromTag(tag string) string {
	first, _, _ := strings.Cut(tag, "-")
	return "skia-" + first
}

func wantsSourceFile(sourceDir, rel string) bool {
	fi, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(rel))) //nolint:gosec // path from trusted app logic
	return err == nil && !fi.IsDir()
}

// ContractFingerprint renders a compact, log-friendly summary of the
// contract used for install-state records.
func (c Contract) ContractFingerprint() string {
	parts := []string{
		fmt.Sprintf("level=%d", c.Level),
		"cmake-min=" + c.CMakeMinVersion.String(),
		"skia=" + c.SkiaTag,
	}
	if c.BundleOutput != "" {
		parts = append(parts, "bundle="+filepath.ToSlash(c.BundleOutput))
	}
	return strings.Join(parts, " ")
}

// SourceArchiveLayout describes how the official source archive is laid
// out after extraction (flat: no wrapper directory).
const SourceArchiveLayout = "flat"

// ParseSourceZipName extracts the version from an official source zip
// asset name, e.g. "Aseprite-v1.3.18.6-Source.zip" → 1.3.18.6. Used to
// cross-check that a downloaded asset matches the release we resolved.
func ParseSourceZipName(name string) (Version, error) {
	prefix, ok := strings.CutPrefix(name, "Aseprite-v")
	if !ok {
		return Version{}, fmt.Errorf("asset %q is not an official Aseprite source archive", name)
	}
	ver, ok := strings.CutSuffix(prefix, "-Source.zip")
	if !ok {
		return Version{}, fmt.Errorf("asset %q is not an official Aseprite source archive", name)
	}
	return version.Parse(ver)
}
