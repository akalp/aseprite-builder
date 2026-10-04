package aseprite

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// SkiaPlatform is the platform descriptor upstream Skia packaging
// uses. Name is one of "macos"/"linux"/"windows"; CPU one of
// "arm64"/"x64"/"x86".
type SkiaPlatform struct {
	Name string
	CPU  string
}

// SkiaRequirement is the Skia artifact the source tree requires,
// discovered entirely from upstream data in the extracted source:
// the tag from laf/misc/skia-tag.txt and the exact prebuilt package URL
// from laf/misc/skia-url.sh. Nothing is hardcoded here.
type SkiaRequirement struct {
	Tag     string // e.g. "m124-08a5439a6b"
	DirName string // e.g. "skia-m124" (upstream build.sh's derived name)
	URL     string // authoritative aseprite/skia release download URL
	CPU     string
}

// DiscoverSkiaRequirement runs upstream's own Skia URL resolver script
// in the source tree and validates what it prints.
//
// Running the upstream script (rather than reproducing its logic) is
// the contract-faithful approach: if upstream changes how Skia is
// resolved, either the script still yields a valid URL (fine) or it
// yields something we reject (safe failure, no guessing).
func DiscoverSkiaRequirement(ctx context.Context, r runner.Runner, sourceDir string, plat SkiaPlatform) (SkiaRequirement, error) {
	tag, err := discoverSkiaTag(sourceDir)
	if err != nil {
		return SkiaRequirement{}, err
	}

	script := filepath.Join(sourceDir, "laf", "misc", "skia-url.sh")
	res, err := r.Run(ctx, runner.Command{
		Name: "bash",
		Args: []string{script, "Release", plat.Name, plat.CPU},
		Dir:  sourceDir,
	})
	if err != nil {
		return SkiaRequirement{}, fmt.Errorf("resolve Skia URL with upstream script: %w", err)
	}
	if !res.OK() {
		return SkiaRequirement{}, fmt.Errorf("upstream Skia URL resolver exited with code %d", res.ExitCode)
	}
	rawURL := strings.TrimSpace(res.Stdout)
	if rawURL == "" {
		return SkiaRequirement{}, fmt.Errorf("upstream Skia URL resolver produced no URL")
	}
	if err := validateSkiaURL(rawURL, tag); err != nil {
		return SkiaRequirement{}, err
	}

	return SkiaRequirement{
		Tag:     tag,
		DirName: skiaDirNameFromTag(tag),
		URL:     rawURL,
		CPU:     plat.CPU,
	}, nil
}

// validateSkiaURL enforces the safety shape every resolved Skia URL
// must satisfy: https, the aseprite/skia release domain, and the exact
// tag read from skia-tag.txt.
func validateSkiaURL(rawURL, tag string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("upstream Skia URL %q is not a valid URL: %w", rawURL, err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("upstream Skia URL %q is not https", rawURL)
	}
	// Cross-check: the URL must reference the aseprite/skia releases
	// domain and embed the exact tag read from skia-tag.txt.
	host := strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
	if host != "github.com" {
		return fmt.Errorf("upstream Skia URL %q does not point at github.com", rawURL)
	}
	prefix := "/aseprite/skia/releases/download/" + tag + "/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return fmt.Errorf(
			"upstream Skia URL %q does not match the required tag %q from laf/misc/skia-tag.txt", rawURL, tag)
	}
	return nil
}

// skiaURLTemplateRe extracts the URL template line skia-url.sh echoes:
//
//	echo https://github.com/aseprite/skia/releases/download/$skia_tag/$skia_file
//
// The template is the contract fact; everything downstream
// substitution touches ($skia_tag, $skia_file) is derived from
// upstream data, never from a builder-side matrix.
var skiaURLTemplateRe = regexp.MustCompile(`echo\s+https://github\.com/aseprite/skia/releases/download/\$skia_tag/\$skia_file\b`)

// skiaFileNameRes match the per-OS asset-name lines the script
// assigns to skia_file, e.g.
//
//	skia_file=Skia-Windows-$skia_build-$cpu.zip
//
// The constant prefix ("Skia-Windows-" etc.) and the ".zip" suffix are
// pinned exactly as written in the script; if upstream reshapes the
// line, parsing fails and the builder fails safely instead of
// guessing an asset name.
var skiaFileNameRes = map[string]*regexp.Regexp{
	"macos":   regexp.MustCompile(`(?m)^\s*skia_file=Skia-macOS-\$skia_build-\$cpu\.zip\s*$`),
	"windows": regexp.MustCompile(`(?m)^\s*skia_file=Skia-Windows-\$skia_build-\$cpu\.zip\s*$`),
	"linux":   regexp.MustCompile(`(?m)^\s*skia_file=Skia-Linux-\$skia_build-\$cpu\.zip\s*$`),
}

// skiaOSProductTokens are the exact asset-name tokens skia-url.sh
// uses per recognized platform: the script's own lines define
// Skia-Windows-…, Skia-macOS-… and Skia-Linux-…. The map keys follow
// the builder's canonical names (skiaOSName semantics).
var skiaOSProductTokens = map[string]string{
	"macos":   "macOS",
	"windows": "Windows",
	"linux":   "Linux",
}

// DiscoverSkiaRequirementBashFree derives the Skia download URL when
// bash cannot be executed (Windows). It reads the same two facts the
// script itself encodes — the URL skeleton and the per-OS asset name
// template — directly out of laf/misc/skia-url.sh, substitutes the tag
// (from skia-tag.txt), the release build and the CPU, and validates
// the result against the same safety shape as the bash path.
//
// The release build is fixed to "Release": the builder only builds
// release-mode Aseprite (build.sh assigns RelWithDebInfo for users and
// the prebuilt Skia Debug package exists solely for Debug builds).
func DiscoverSkiaRequirementBashFree(sourceDir string, plat SkiaPlatform) (SkiaRequirement, error) {
	tag, err := discoverSkiaTag(sourceDir)
	if err != nil {
		return SkiaRequirement{}, err
	}

	data, err := os.ReadFile(filepath.Join(sourceDir, "laf", "misc", "skia-url.sh")) //nolint:gosec // path from trusted app logic
	if err != nil {
		return SkiaRequirement{}, fmt.Errorf("cannot read the upstream Skia URL resolver script: %w", err)
	}
	script := string(data)

	if !skiaURLTemplateRe.MatchString(script) {
		return SkiaRequirement{}, fmt.Errorf(
			"skia-url.sh no longer contains the expected release URL template; " +
				"the upstream Skia resolution changed shape")
	}
	nameRe, ok := skiaFileNameRes[plat.Name]
	if !ok {
		return SkiaRequirement{}, fmt.Errorf("unknown Skia platform %q", plat.Name)
	}
	if !nameRe.MatchString(script) {
		return SkiaRequirement{}, fmt.Errorf(
			"skia-url.sh no longer contains the expected %s asset name template", plat.Name)
	}

	skiaFile := "Skia-" + skiaOSProductTokens[plat.Name] + "-Release-" + plat.CPU + ".zip"
	rawURL := "https://github.com/aseprite/skia/releases/download/" + tag + "/" + skiaFile
	if err := validateSkiaURL(rawURL, tag); err != nil {
		return SkiaRequirement{}, err
	}
	return SkiaRequirement{
		Tag:     tag,
		DirName: skiaDirNameFromTag(tag),
		URL:     rawURL,
		CPU:     plat.CPU,
	}, nil
}
