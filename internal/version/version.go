// Package version implements numeric dotted version comparison shared
// by Aseprite releases and build-tool versions (CMake, Ninja).
//
// Versions use dot-separated numeric components of variable length;
// a missing component compares as zero (1.3 == 1.3.0).
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed dotted-numeric version like "1.3.18.6".
type Version struct {
	// Parts are the numeric components.
	Parts []int
	// Raw is the canonical form without any leading "v" or whitespace.
	Raw string
}

// Parse parses "v1.3.18.6" or "1.3.18.6" into a Version.
func Parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return Version{}, fmt.Errorf("parse version %q: empty", s)
	}
	parts := strings.Split(s, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("parse version %q: non-numeric component %q", s, p)
		}
		nums = append(nums, n)
	}
	return Version{Parts: nums, Raw: s}, nil
}

// String renders the canonical dot-separated form.
func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	parts := make([]string, len(v.Parts))
	for i, p := range v.Parts {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ".")
}

// IsZero reports whether the version has no numeric parts.
func (v Version) IsZero() bool { return len(v.Parts) == 0 }

// Compare returns -1, 0 or 1 comparing v against other by numeric
// parts, treating a missing part as 0.
func (v Version) Compare(other Version) int {
	n := max(len(v.Parts), len(other.Parts))
	for i := 0; i < n; i++ {
		a, b := 0, 0
		if i < len(v.Parts) {
			a = v.Parts[i]
		}
		if i < len(other.Parts) {
			b = other.Parts[i]
		}
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
	}
	return 0
}

// Less reports whether v sorts before other.
func (v Version) Less(other Version) bool { return v.Compare(other) < 0 }

// AtLeast reports whether v satisfies the minimum required version min
// (missing parts in min count as zero).
func (v Version) AtLeast(min Version) bool { return v.Compare(min) >= 0 }
