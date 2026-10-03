package aseprite

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
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
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return SkiaRequirement{}, fmt.Errorf("upstream Skia URL %q is not a valid URL: %w", rawURL, err)
	}
	if parsed.Scheme != "https" {
		return SkiaRequirement{}, fmt.Errorf("upstream Skia URL %q is not https", rawURL)
	}
	// Cross-check: the URL must reference the aseprite/skia releases
	// domain and embed the exact tag read from skia-tag.txt.
	host := strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
	if host != "github.com" {
		return SkiaRequirement{}, fmt.Errorf("upstream Skia URL %q does not point at github.com", rawURL)
	}
	prefix := "/aseprite/skia/releases/download/" + tag + "/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return SkiaRequirement{}, fmt.Errorf(
			"upstream Skia URL %q does not match the required tag %q from laf/misc/skia-tag.txt", rawURL, tag)
	}

	return SkiaRequirement{
		Tag:     tag,
		DirName: skiaDirNameFromTag(tag),
		URL:     rawURL,
		CPU:     plat.CPU,
	}, nil
}
