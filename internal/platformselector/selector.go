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

// NewForRunningSystem returns the concrete Platform implementation for
// runtime.GOOS.
func NewForRunningSystem(builderVersion string) (platform.Platform, error) {
	return NewForOS(runtime.GOOS, builderVersion)
}

// NewForOS returns the concrete Platform for a canonical OS name.
func NewForOS(canonicalOS, builderVersion string) (platform.Platform, error) {
	switch canonicalOS {
	case "darwin":
		return newDarwinPlatform(builderVersion)
	default:
		return nil, ErrUnsupportedPlatform
	}
}
