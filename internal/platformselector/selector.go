// Package platformselector maps the runtime OS to its concrete
// platform.Platform implementation.
//
// This is the ONLY place in the codebase where the operating system is
// selected: every other component goes through the platform.Platform
// interface. Platform implementation packages define their own build
// constraints, so unsupported OSes do not compile host-specific code.
package platformselector

import (
	"errors"
	"runtime"

	"github.com/akalp/aseprite-builder/internal/platform"
)

// ErrUnsupportedPlatform is returned for OSes the builder does not
// support yet. It surfaces as a clear user-facing message instead of a
// crash.
var ErrUnsupportedPlatform = errors.New("this operating system is not supported yet by aseprite-builder")

// selectorConstructor builds the concrete Platform implementation for
// one OS.
type selectorConstructor func(builderVersion string) (platform.Platform, error)

// selectors maps a canonical OS name to its constructor. It is
// populated by init() in the per-host selector files
// (selector_darwin.go on darwin, selector_windows.go on windows). Each
// file is GOOS-gated, so a host only ever registers its own platform:
// other hosts' constructor files do not compile here.
var selectors = map[string]selectorConstructor{}

// registerSelector is called from per-host init() functions.
func registerSelector(canonicalOS string, c selectorConstructor) {
	selectors[canonicalOS] = c
}

// NewForRunningSystem returns the concrete Platform implementation for
// runtime.GOOS.
func NewForRunningSystem(builderVersion string) (platform.Platform, error) {
	return NewForOS(runtime.GOOS, builderVersion)
}

// NewForOS returns the concrete Platform for a canonical OS name.
// Unknown names yield ErrUnsupportedPlatform, which the frontends map
// to a friendly message.
func NewForOS(canonicalOS, builderVersion string) (platform.Platform, error) {
	c, ok := selectors[canonicalOS]
	if !ok {
		return nil, ErrUnsupportedPlatform
	}
	return c(builderVersion)
}
