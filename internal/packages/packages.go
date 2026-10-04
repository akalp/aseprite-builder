// Package packages abstracts the Linux distribution package managers
// used to detect (and, only after explicit user consent, install) the
// system-managed build dependencies Aseprite requires.
//
// Compilers and development libraries remain distribution-managed
// (spec §16): the builder never bundles .deb/.rpm packages, never adds
// package repositories, and never elevates privileges on its own.
//
// The package carries no version information — only package names,
// documented alongside each manager implementation. Package names for
// the host distribution are the sanctioned exception to the
// no-version-matrices rule (spec §2/§5): names are stable platform
// knowledge, versions are always whatever the distribution provides.
package packages

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Package is one distribution package the builder may need.
type Package struct {
	// Name is the distribution's package name (e.g. "ninja-build").
	Name string
	// What explains, in a few words, why the package is required.
	// Used in user-facing requirement details.
	What string
}

// PackageManager is the per-distribution package backend (spec §22).
type PackageManager interface {
	// Detect reports whether this backend applies to the host
	// distribution and is executable.
	Detect(ctx context.Context) bool
	// MissingPackages returns the subset of pkgs that is not currently
	// installed. It never installs anything and never assumes versions.
	MissingPackages(ctx context.Context, pkgs []Package) ([]Package, error)
	// Install installs exactly pkgs through the distribution's native
	// tool. Callers invoke it only after explicit user consent; the
	// implementation runs visibly and must never strip the terminal or
	// elevate privileges silently (spec §16/§33).
	Install(ctx context.Context, pkgs []Package) error
}

// Names returns the package names of pkgs, for commands and messages.
func Names(pkgs []Package) []string {
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, p.Name)
	}
	return out
}

// Report summarizes the system-managed package situation on the host.
// An empty Manager means the distribution has no supported package
// backend (fail-safe: no package operations are offered).
type Report struct {
	// Manager is the selected backend name ("apt"), or "" when the
	// distribution is not supported by this release.
	Manager string
	// Missing lists the required packages that are not installed.
	Missing []Package
	// Command is the exact copy-paste command that installs Missing.
	Command string
}

// OSRelease holds the /etc/os-release fields the builder uses to
// identify the distribution. Values are recorded exactly as upstream
// writes them (IDs are lowercase by convention).
type OSRelease struct {
	ID              string   // e.g. "ubuntu"
	IDLike          []string // e.g. ["debian"]
	VersionID       string   // e.g. "24.04"
	VersionCodename string   // e.g. "noble"
	PrettyName      string   // e.g. "Ubuntu 24.04.1 LTS"
}

// IsDebianLike reports whether the release identifies as Debian,
// Ubuntu, or a derivative of either (ID or any ID_LIKE entry). Ubuntu
// derivatives carry ID_LIKE=ubuntu, so both roots are recognized.
func (r OSRelease) IsDebianLike() bool {
	return matchesFamily(r, "debian") || matchesFamily(r, "ubuntu")
}

// matchesFamily reports whether id appears as the release ID or among
// its ID_LIKE chain roots.
func matchesFamily(r OSRelease, id string) bool {
	if strings.EqualFold(r.ID, id) {
		return true
	}
	for _, like := range r.IDLike {
		if strings.EqualFold(like, id) {
			return true
		}
	}
	return false
}

// ErrUnsupportedDistro reports a Linux distribution with no supported
// package backend. Detection and requirement checks fail safely with
// it instead of guessing (spec §11: only the listed distributions are
// supported).
var ErrUnsupportedDistro = errors.New("unsupported Linux distribution")

// ParseOSRelease parses os-release content (shell-style KEY=value
// lines, '#' comments, optional single or double quoting). Unknown
// keys are ignored and malformed lines are skipped: a partial parse
// must never take down detection, and only a completely unidentifiable
// release is treated as unknown by callers.
func ParseOSRelease(data string) OSRelease {
	var rel OSRelease
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = unquoteOSValue(strings.TrimSpace(value))
		switch key {
		case "ID":
			rel.ID = value
		case "ID_LIKE":
			rel.IDLike = strings.Fields(value)
		case "VERSION_ID":
			rel.VersionID = value
		case "VERSION_CODENAME":
			rel.VersionCodename = value
		case "PRETTY_NAME":
			rel.PrettyName = value
		}
	}
	return rel
}

// unquoteOSValue applies os-release quoting rules: double quotes allow
// backslash escapes, single quotes are literal, unquoted values are
// taken verbatim.
func unquoteOSValue(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if unquoted, err := strconv.Unquote(v); err == nil {
			return unquoted
		}
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}

// Display renders the distribution for logs and diagnostics.
func (r OSRelease) Display() string {
	if r.PrettyName != "" {
		return r.PrettyName
	}
	switch {
	case r.ID != "" && r.VersionID != "":
		return r.ID + " " + r.VersionID
	case r.ID != "":
		return r.ID
	default:
		return "unknown"
	}
}
