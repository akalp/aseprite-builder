// Package aseprite holds Aseprite domain models: versions, releases,
// the official source contract, Skia requirement discovery, and build
// orchestration, without keeping a version-specific matrix in this
// codebase.
package aseprite

import (
	"fmt"
	"strings"

	"github.com/aseprite-builder/aseprite-builder/internal/version"
)

// Version is Aseprite's dotted-numeric version ("1.3.18.6"). It shares
// the generic dotted-numeric comparison with tool versions.
type Version = version.Version

// ParseVersion parses "v1.3.18.6" or "1.3.18.6".
func ParseVersion(s string) (Version, error) {
	return version.Parse(s)
}

// Release describes one official Aseprite release fetched from
// upstream metadata (GitHub releases API, or an equivalent source).
//
// Only fields the builder actually relies on are tracked; anything
// else is treated as opaque upstream data and never used for
// version-specific decisions.
type Release struct {
	Version Version
	// TagName is the raw upstream tag, e.g. "v1.3.18.6".
	TagName string
	// SourceURL points at the official source archive. Expected shape:
	// https://github.com/aseprite/aseprite/releases/download/<tag>/Aseprite-v<ver>-Source.zip
	SourceURL string
	// PreRelease and Draft mirror upstream release flags.
	PreRelease bool
	Draft      bool
	// SHA256 is the archive digest when upstream metadata provides one
	// (empty when it does not).
	SHA256 string
	// AssetSize is the byte size of the source archive when known.
	AssetSize int64
}

// SourceAssetName returns the expected source.zip asset name for this
// release. Derived from the tag, so no version matrix is needed.
func (r Release) SourceAssetName() string {
	tag := strings.TrimPrefix(r.TagName, "v")
	return fmt.Sprintf("Aseprite-v%s-Source.zip", tag)
}

// Stable reports whether the release is a normal stable channel
// release: published, not a draft, not a pre-release.
func (r Release) Stable() bool { return !r.PreRelease && !r.Draft }

// String renders a human-readable summary used in logs.
func (r Release) String() string {
	return fmt.Sprintf("Aseprite %s (%s)", r.Version, r.TagName)
}
