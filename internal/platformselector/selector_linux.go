//go:build linux

package platformselector

import (
	"fmt"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/platform/linux"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// This file registers the linux selector when GOOS=linux (see
// selector.go). Distribution packages are detected through the package
// managers layer and remain system-managed; the builder never elevates
// privileges on its own.
func init() { registerSelector("linux", newLinuxPlatform) }

// newLinuxPlatform builds the Linux Platform implementation.
func newLinuxPlatform(builderVersion string) (platform.Platform, error) {
	r := runner.NewConcreteRunner()
	p, err := linux.New(r, builderVersion)
	if err != nil {
		return nil, fmt.Errorf("linux platform: %w", err)
	}
	return p, nil
}
