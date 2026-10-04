//go:build darwin

package platformselector

import (
	"fmt"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/platform/darwin"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// This file registers the darwin selector when GOOS=darwin (see
// selector.go). The Apple system-managed toolchain is detected, not
// installed.
func init() { registerSelector("darwin", newDarwinPlatform) }

// newDarwinPlatform builds the macOS Platform implementation.
func newDarwinPlatform(builderVersion string) (platform.Platform, error) {
	r := runner.NewConcreteRunner()
	p, err := darwin.New(r, builderVersion)
	if err != nil {
		return nil, fmt.Errorf("darwin platform: %w", err)
	}
	return &p, nil
}
