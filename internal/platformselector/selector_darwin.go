//go:build darwin

package platformselector

import (
	"fmt"

	"github.com/aseprite-builder/aseprite-builder/internal/platform"
	"github.com/aseprite-builder/aseprite-builder/internal/platform/darwin"
	"github.com/aseprite-builder/aseprite-builder/internal/runner"
)

// newDarwinPlatform builds the macOS Platform implementation. The
// Apple system-managed toolchain is detected, not installed.
func newDarwinPlatform(builderVersion string) (platform.Platform, error) {
	r := runner.NewConcreteRunner()
	p, err := darwin.New(r, builderVersion)
	if err != nil {
		return nil, fmt.Errorf("darwin platform: %w", err)
	}
	return p, nil
}
