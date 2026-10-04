package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/akalp/aseprite-builder/internal/logging"
	"github.com/akalp/aseprite-builder/internal/platform"
)

// installRootFor derives the platform installation root from an
// installed application path. Every supported layout puts the
// application directly under the root (Aseprite.app, Aseprite/,
// aseprite/), so the parent directory is the root.
func installRootFor(appPath string) string {
	if appPath == "" {
		return ""
	}
	return filepath.Dir(appPath)
}

// sameDir compares cleaned paths, case-insensitively like Windows
// filesystems do.
func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// applyInstallPreferences applies the advanced per-run preferences
// (spec §41) to the platform before any requirement checks run — the
// disk-space check must see the real installation target — and logs
// every applied preference so the operation log explains the behavior.
func (s *Service) applyInstallPreferences(opts InstallOptions, log *logging.Logger) error {
	dir := strings.TrimSpace(opts.InstallDir)
	if dir != "" && !sameDir(dir, filepath.Dir(s.opts.Platform.DefaultInstallPath())) {
		setter, ok := s.opts.Platform.(platform.InstallRootSetter)
		if !ok {
			return fmt.Errorf("this platform does not support a custom install directory")
		}
		if err := setter.SetInstallRoot(dir); err != nil {
			return err
		}
		log.Line("install root: %s", dir)
	}
	if opts.RetainBuilds != nil && !*opts.RetainBuilds {
		if setter, ok := s.opts.Platform.(platform.RetentionSetter); ok {
			setter.SetRetainPrevious(false)
			log.Line("previous-build retention disabled by preference")
		}
	}
	return nil
}
