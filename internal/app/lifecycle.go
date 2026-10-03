package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aseprite-builder/aseprite-builder/internal/aseprite"
	"github.com/aseprite-builder/aseprite-builder/internal/logging"
	"github.com/aseprite-builder/aseprite-builder/internal/paths"
	"github.com/aseprite-builder/aseprite-builder/internal/platform"
	"github.com/aseprite-builder/aseprite-builder/internal/state"
)

// Update installs the latest stable Aseprite version over the current
// installation. The pipeline is identical to install — the transactional
// platform install keeps the working version intact until the new build
// verifies.
func (s *Service) Update(ctx context.Context, opts InstallOptions, sink ProgressSink) (InstallResult, error) {
	current, err := s.opts.Store.LoadInstall()
	if errors.Is(err, state.ErrNoState) {
		return InstallResult{}, ErrNotInstalled
	}
	if err != nil {
		return InstallResult{}, err
	}
	log, logPath, err := logging.NewFile(s.logDir(), "update")
	if err != nil {
		return InstallResult{}, err
	}
	defer func() { _ = log.Close() }()
	log.Line("update: installed=%s", current.AsepriteVersion)

	// Resolve the release first so "nothing newer" short-circuits
	// before any build work happens.
	target, err := s.resolveRelease(ctx, opts)
	if err != nil {
		return InstallResult{}, err
	}
	if target.Version.String() == current.AsepriteVersion {
		return InstallResult{Version: current.AsepriteVersion, AppPath: current.AppPath, LogPath: logPath}, ErrUpToDate
	}
	log.Line("update: %s -> %s", current.AsepriteVersion, target.Version)

	result, err := s.runPipeline(ctx, opts, sink, log)
	result.LogPath = logPath
	if err != nil {
		log.Line("update failed: %v", err)
		return result, err
	}
	log.Line("update completed: %s", result.Version)
	return result, nil
}

// RebuildOptions selects between the two rebuild modes: RebuildCurrent
// recompiles the installed version; Clean discards the existing build
// directory first.
type RebuildOptions struct {
	Clean bool
}

// Rebuild recompiles the currently installed Aseprite version from its
// source, preserving user data (which lives in Application Support,
// outside the build and bundle), then swaps the installation after
// verification. A failed rebuild leaves the current installation
// untouched.
func (s *Service) Rebuild(ctx context.Context, opts RebuildOptions, sink ProgressSink) (InstallResult, error) {
	current, err := s.opts.Store.LoadInstall()
	if errors.Is(err, state.ErrNoState) {
		return InstallResult{}, ErrNotInstalled
	}
	if err != nil {
		return InstallResult{}, err
	}
	log, logPath, err := logging.NewFile(s.logDir(), "rebuild")
	if err != nil {
		return InstallResult{}, err
	}
	defer func() { _ = log.Close() }()
	log.Line("rebuild: version=%s clean=%v", current.AsepriteVersion, opts.Clean)

	installOpts := InstallOptions{
		Version:               current.AsepriteVersion,
		Channel:               stateChannelOrDefault(current.Channel),
		ForceOverwriteUnowned: true, // target is (or was) builder-owned
	}
	if opts.Clean {
		// Discard previous build output AND the extracted source tree so
		// the rebuild starts from a fresh, verified extraction. User data
		// (Application Support) is outside the build and bundle and is
		// therefore never touched.
		if current.WorkspacePath != "" {
			if err := os.RemoveAll(current.WorkspacePath); err != nil {
				log.Line("note: could not clear workspace %s: %v", current.WorkspacePath, err)
			}
		}
	}
	result, err := s.runPipeline(ctx, installOpts, sink, log)
	result.LogPath = logPath
	if err != nil {
		log.Line("rebuild failed: %v", err)
		return result, err
	}
	log.Line("rebuild completed: %s", result.Version)
	return result, nil
}

// Repair validates the current installation and rebuilds it when the
// bundle is broken (missing executable/version mismatch/marker absent).
// Actions are always explicit and logged.
func (s *Service) Repair(ctx context.Context, sink ProgressSink) (InstallResult, error) {
	current, err := s.opts.Store.LoadInstall()
	if errors.Is(err, state.ErrNoState) {
		return InstallResult{}, ErrNotInstalled
	}
	if err != nil {
		return InstallResult{}, err
	}
	log, logPath, err := logging.NewFile(s.logDir(), "repair")
	if err != nil {
		return InstallResult{}, err
	}
	defer func() { _ = log.Close() }()
	log.Line("repair: checking installation at %s", current.AppPath)

	if healthy := s.installationHealthy(ctx, current, log); healthy {
		log.Line("repair: installation looks healthy; nothing to do")
		return InstallResult{Version: current.AsepriteVersion, AppPath: current.AppPath, LogPath: logPath}, nil
	}

	log.Line("repair: installation is damaged; rebuilding %s", current.AsepriteVersion)
	result, err := s.runPipeline(ctx, InstallOptions{
		Version:               current.AsepriteVersion,
		Channel:               stateChannelOrDefault(current.Channel),
		ForceOverwriteUnowned: true,
	}, sink, log)
	result.LogPath = logPath
	if err != nil {
		log.Line("repair rebuild failed: %v", err)
		return result, err
	}
	log.Line("repair completed")
	return result, nil
}

// installationHealthy checks the installed bundle markers.
func (s *Service) installationHealthy(ctx context.Context, current state.InstallState, log *logging.Logger) bool {
	info, err := os.Stat(current.AppPath)
	if err != nil || !info.IsDir() {
		log.Line("repair: app bundle missing or unreadable: %v", err)
		return false
	}
	if err := s.verifyInstalledBundle(ctx, current, log); err != nil {
		return false
	}
	return true
}

// UninstallOptions selects the scope of uninstall.
type UninstallOptions = platform.UninstallOptions

// Uninstall removes the builder-owned application. Only provably
// builder-owned files are touched.
func (s *Service) Uninstall(_ context.Context, opts UninstallOptions) error {
	_, err := s.opts.Store.LoadInstall()
	if errors.Is(err, state.ErrNoState) {
		return ErrNotInstalled
	}
	if err != nil {
		return err
	}
	if err := s.opts.Platform.Uninstall(context.Background(), opts); err != nil {
		if errors.Is(err, platform.ErrNotOwned) {
			return err
		}
		return fmt.Errorf("uninstall failed: %w", err)
	}
	return s.opts.Store.ClearInstall()
}

// Launch starts the installed application.
func (s *Service) Launch(ctx context.Context) error {
	_, err := s.opts.Store.LoadInstall()
	if errors.Is(err, state.ErrNoState) {
		return ErrNotInstalled
	}
	if err != nil {
		return err
	}
	return s.opts.Platform.Launch(ctx)
}

// Layout exposes the resolved user-local directory layout for CLI
// display and purge options.
func (s *Service) Layout() paths.Layout { return s.opts.Layout }

// CurrentInstall returns the install state when present ("" version
// otherwise). Used by `version` and the TUI home screen.
func (s *Service) CurrentInstall() (state.InstallState, bool, error) {
	inst, err := s.opts.Store.LoadInstall()
	if errors.Is(err, state.ErrNoState) {
		return state.InstallState{}, false, nil
	}
	if err != nil {
		return state.InstallState{}, false, err
	}
	return inst, true, nil
}

// LatestStable reports the newest upstream stable release (used by the
// TUI home screen and update checks).
func (s *Service) LatestStable(ctx context.Context) (string, error) {
	rel, err := s.opts.Releases.Resolve(ctx, aseprite.ChannelStable)
	if err != nil {
		return "", err
	}
	return rel.Version.String(), nil
}
