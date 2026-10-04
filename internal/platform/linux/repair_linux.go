//go:build linux

package linux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/akalp/aseprite-builder/internal/platform"
)

// RepairTargeted implements platform.Repairer: stale staging cleanup
// plus recreation of the user-local CLI symlink, desktop entry and
// icons. Only builder-owned files are touched; conflicting foreign
// files are reported, never overwritten.
func (p Platform) RepairTargeted(ctx context.Context, req platform.RepairRequest) ([]string, error) {
	var actions []string

	staging := filepath.Join(p.InstallRoot, retentionDirName, "staging")
	if _, err := os.Stat(staging); err == nil {
		if err := os.RemoveAll(staging); err != nil {
			return actions, err
		}
		actions = append(actions, "removed staging left by an interrupted update")
	} else if !errors.Is(err, os.ErrNotExist) {
		return actions, err
	}

	link := filepath.Join(p.CLIBinDir, exeName)
	if p.CLIBinDir != "" && linkNeedsRepair(link, p.exePath()) {
		if err := p.linkCLI(); err != nil {
			return actions, err
		}
		actions = append(actions, "recreated the CLI symlink "+link)
	}

	if p.DataHome != "" {
		if _, err := os.ReadFile(p.desktopEntryPath()); errors.Is(err, os.ErrNotExist) {
			template := ""
			if req.SourceDir != "" {
				template = filepath.Join(req.SourceDir, "src", "desktop", "linux", "aseprite.desktop")
			}
			if err := p.writeDesktopEntry(template); err != nil {
				return actions, err
			}
			actions = append(actions, "recreated the desktop entry "+p.desktopEntryPath())
		}
		if req.SourceDir != "" && !p.hasIcons() {
			if err := p.installIcons(filepath.Join(req.SourceDir, "data", "icons")); err != nil {
				p.note("note: icons not reinstalled: %v", err)
			} else {
				actions = append(actions, "reinstalled the hicolor icons")
			}
		}
		if len(actions) > 0 {
			p.runQuiet(ctx, "update-desktop-database", p.applicationsDir())
			p.runQuiet(ctx, "gtk-update-icon-cache", "-q", p.iconThemeDir())
		}
	}
	return actions, nil
}

// CheckIntegration implements platform.IntegrationDoctor: it validates
// the CLI symlink, desktop entry and icons without fixing anything.
func (p Platform) CheckIntegration() (bool, string) {
	var issues []string
	if p.CLIBinDir != "" {
		link := filepath.Join(p.CLIBinDir, exeName)
		info, err := os.Lstat(link)
		switch {
		case err != nil:
			issues = append(issues, "CLI symlink missing")
		case info.Mode()&os.ModeSymlink == 0:
			issues = append(issues, "CLI path is not a symlink")
		default:
			target, err := os.Readlink(link)
			if err != nil || !sameLinkTarget(link, target, p.exePath()) {
				issues = append(issues, "CLI symlink points elsewhere")
			}
		}
	}
	if p.DataHome != "" {
		data, err := os.ReadFile(p.desktopEntryPath())
		switch {
		case errors.Is(err, os.ErrNotExist):
			issues = append(issues, "desktop entry missing")
		case err != nil:
			issues = append(issues, "desktop entry unreadable")
		case !strings.Contains(string(data), desktopMarker):
			issues = append(issues, "desktop entry not managed by aseprite-builder")
		}
		if !p.hasIcons() {
			issues = append(issues, "icons missing")
		}
	}
	if len(issues) > 0 {
		return false, strings.Join(issues, ", ") + " — run repair"
	}
	return true, "CLI symlink, desktop entry and icons valid"
}

// linkNeedsRepair reports whether link is absent or does not resolve to
// desired. Real files are never ours to repair (linkCLI protects them).
func linkNeedsRepair(link, desired string) bool {
	info, err := os.Lstat(link)
	if err != nil {
		return true
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(link)
	if err != nil {
		return true
	}
	return !sameLinkTarget(link, target, desired)
}

// hasIcons reports whether at least one installed hicolor size carries
// the application icon.
func (p Platform) hasIcons() bool {
	root := p.iconThemeDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && iconSizeDirRe.MatchString(e.Name()) {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "apps", exeName+".png")); err == nil {
				return true
			}
		}
	}
	return false
}
