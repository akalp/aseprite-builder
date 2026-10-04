//go:build windows

package platformselector

import (
	"fmt"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/platform/windows"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// This file registers the windows selector when GOOS=windows (see
// selector.go). The Microsoft system-managed toolchain is detected
// (vswhere/vcvars), not installed; the build environment is captured
// from vcvars64.bat per build.
func init() { registerSelector("windows", newWindowsPlatform) }

// newWindowsPlatform builds the Windows Platform implementation.
func newWindowsPlatform(builderVersion string) (platform.Platform, error) {
	r := runner.NewConcreteRunner()
	p, err := windows.New(r, builderVersion)
	if err != nil {
		return nil, fmt.Errorf("windows platform: %w", err)
	}
	return p, nil
}
