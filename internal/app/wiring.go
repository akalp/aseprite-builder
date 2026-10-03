package app

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/platformselector"
	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/state"
)

// NewDefault constructs the application service from the running
// system's real infrastructure: user-local paths, the platform
// implementation selected for this OS, the official GitHub release
// provider, a concrete process runner and the state store.
//
// Both frontends (CLI and TUI) construct their service here so the
// wiring cannot drift between them.
//
// verifyPlatform mirrors the CLI behavior: doctor-style diagnostics
// pass false so they work even on platforms without an install
// pipeline yet; operations that build pass true and fail fast on
// unsupported systems.
func NewDefault(builderVersion string, verifyPlatform bool) (*Service, error) {
	layout, err := paths.New("")
	if err != nil {
		return nil, err
	}
	if err := layout.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("prepare user directories: %w", err)
	}
	plat, err := platformselector.NewForRunningSystem(builderVersion)
	if verifyPlatform && err != nil {
		if errors.Is(err, platformselector.ErrUnsupportedPlatform) {
			return nil, fmt.Errorf("aseprite-builder does not support %s yet", runtime.GOOS)
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	return New(Options{
		BuilderVersion: builderVersion,
		Layout:         layout,
		Platform:       plat,
		Releases:       aseprite.NewGithubReleaseProvider(),
		Runner:         runner.NewConcreteRunner(),
		Store:          state.Store{Dir: layout.State},
	})
}
