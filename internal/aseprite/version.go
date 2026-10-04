// Package aseprite holds Aseprite domain models: versions, releases,
// the official source contract, Skia requirement discovery, and build
// orchestration, without keeping a version-specific matrix in this
// codebase.
package aseprite

import (
	"fmt"
	"strings"

	"github.com/akalp/aseprite-builder/internal/version"
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
	// Commit is the explicit development-channel commit SHA this
	// release tracks ("" for stable releases).
	Commit string
}

// DevCommit reports whether this release tracks an explicit commit
// (development channel) instead of a stable release archive.
func (r Release) DevCommit() bool { return r.Commit != "" }

// BuildVersion is the version string used for the source tree name,
// logs and install state: the dotted release version for stable, or
// "dev-<short>" for a development commit. Development builds report
// their own upstream version at runtime, so this is a builder-side
// identifier, not a version assertion.
func (r Release) BuildVersion() string {
	if r.Commit != "" {
		return "dev-" + ShortCommit(r.Commit)
	}
	return r.Version.String()
}

// ShortCommit returns the 7-character display form of a commit SHA.
func ShortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// ValidCommit reports whether s is a plausible git commit SHA:
// 7-40 hexadecimal characters.
func ValidCommit(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
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
	if r.Commit != "" {
		return "Aseprite development commit " + ShortCommit(r.Commit)
	}
	return fmt.Sprintf("Aseprite %s (%s)", r.Version, r.TagName)
}
