//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// Install activates a verified build output transactionally:
//
//  1. copy the build's bin payload into a staging dir under Programs
//  2. write the ownership marker and the Start Menu shortcut
//  3. verify the staged executable (--version) BEFORE touching target
//  4. move any existing (builder-owned) installation into retention
//  5. rename the staged directory into place (atomic on same volume)
//
// On any failure before activation the previous installation stays
// exactly as it was.
func (p Platform) Install(ctx context.Context, out platform.BuildOutput) (platform.InstallOutcome, error) {
	if out.BinDir == "" {
		return platform.InstallOutcome{}, errors.New("build output has no installable directory")
	}
	if fi, err := os.Stat(filepath.Join(out.BinDir, exeName)); err != nil || fi.IsDir() {
		return platform.InstallOutcome{}, fmt.Errorf(
			"built payload %s has no %s: %w", out.BinDir, exeName, err)
	}

	target := p.DefaultInstallPath()
	if err := os.MkdirAll(p.ProgramsRoot, 0o755); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("create install dir: %w", err)
	}
	if _, err := os.Stat(target); err == nil && !p.allowOverwrite(target) {
		return platform.InstallOutcome{}, fmt.Errorf("%s exists and is not managed by aseprite-builder; "+
			"pass --force to replace it explicitly", target)
	}

	// Staging under the install root: same filesystem, so activation
	// is a rename (atomic swap candidate).
	stagingRoot := filepath.Join(p.ProgramsRoot, retentionDirName, "staging")
	if err := os.RemoveAll(stagingRoot); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("clear staging dir: %w", err)
	}
	staged := filepath.Join(stagingRoot, installDirName)
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("create staging dir: %w", err)
	}
	if err := copyDir(out.BinDir, staged); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("stage installation: %w", err)
	}

	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	if err := writeMarker(staged, marker{
		Version:        out.Version,
		BuilderVersion: p.BuilderVersion,
		InstalledAt:    now().UTC(),
	}); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("write ownership marker: %w", err)
	}

	// Verify the staged executable actually runs before replacing
	// anything.
	if err := p.verifyStaged(ctx, staged, out.Version); err != nil {
		_ = os.RemoveAll(stagingRoot)
		return platform.InstallOutcome{}, fmt.Errorf("verify staged application: %w", err)
	}

	// Retain the previous builder-owned installation for rollback.
	previous := ""
	retention := filepath.Join(p.ProgramsRoot, retentionDirName, "previous")
	if _, err := os.Stat(target); err == nil {
		previous = filepath.Join(retention, installDirName)
		if err := os.RemoveAll(previous); err != nil {
			return platform.InstallOutcome{}, fmt.Errorf("clear previous retention: %w", err)
		}
		if err := os.MkdirAll(retention, 0o755); err != nil {
			return platform.InstallOutcome{}, fmt.Errorf("create retention dir: %w", err)
		}
		if err := os.Rename(target, previous); err != nil {
			// A running Aseprite locks the directory on Windows; say so
			// plainly instead of leaking the OS error.
			if strings.Contains(err.Error(), "Access is denied") || strings.Contains(err.Error(), "in use") {
				return platform.InstallOutcome{}, fmt.Errorf(
					"retain previous installation: %s might be running; close Aseprite and retry", target)
			}
			return platform.InstallOutcome{}, fmt.Errorf("retain previous installation: %w", err)
		}
	}
	activated := false
	defer func() {
		if !activated {
			// Rollback: put the previous installation back in place.
			if previous != "" {
				_ = os.Remove(target)
				_ = os.Rename(previous, target)
			}
		}
	}()

	if err := os.Rename(staged, target); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("activate installation: %w", err)
	}
	activated = true

	if err := os.RemoveAll(stagingRoot); err != nil {
		return platform.InstallOutcome{}, fmt.Errorf("clean staging dir: %w", err)
	}

	// Start Menu shortcut (spec §21 optional integration). Failures are
	// non-fatal: launching via aseprite-builder launch always works.
	if p.StartMenuRoot != "" {
		if err := p.writeShortcut(target); err != nil {
			p.note("note: Start Menu shortcut not created: %v", err)
		}
	}

	return platform.InstallOutcome{
		AppPath:        target,
		PreviousPath:   previous,
		StagingRemoved: true,
	}, nil
}

// allowOverwrite reports whether an existing target may be replaced.
func (p Platform) allowOverwrite(target string) bool {
	if _, err := readMarker(target); err == nil {
		return true // builder-owned: safe to replace transactionally
	}
	return p.AllowReplaceUnowned
}

// Launch starts the installed application detached from the console
// (the CLI/TUI must not block on Aseprite's lifetime).
func (p Platform) Launch(ctx context.Context) error {
	target := p.exePath()
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("launch: %s is not installed (%w)", target, err)
	}
	res, err := p.Runner.Run(ctx, runner.Command{
		Name: "cmd",
		Args: []string{"/d", "/c", "start", "", filepath.Base(target)},
		Dir:  filepath.Dir(target),
	})
	if err != nil {
		return fmt.Errorf("launch %s: %w", target, err)
	}
	if !res.OK() {
		return fmt.Errorf("launch %s: start exited with code %d", target, res.ExitCode)
	}
	return nil
}

// Uninstall removes the builder-owned installation and, when asked, the
// retained previous installation and the Start Menu shortcut.
// Ownership is proven via the marker file; without it the operation
// fails rather than guessing (spec §27: never remove unowned files).
func (p Platform) Uninstall(_ context.Context, opts platform.UninstallOptions) error {
	target := p.DefaultInstallPath()
	_, err := readMarker(target)
	switch {
	case err == nil:
		// owned, continue
	case opts.Force:
		// explicit override; allowed but noteworthy
		p.note("uninstall: removing %s without ownership proof (--force)", target)
	default:
		return fmt.Errorf("%w: %s", platform.ErrNotOwned, target)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove %s: %w", target, err)
	}
	if opts.RemovePrevious {
		prev := filepath.Join(p.ProgramsRoot, retentionDirName, "previous", installDirName)
		if err := os.RemoveAll(prev); err != nil {
			return fmt.Errorf("remove previous installation: %w", err)
		}
	}
	if p.StartMenuRoot != "" {
		_ = os.Remove(p.ShortcutPath())
	}
	return nil
}

// InstalledExecutable implements platform.InstalledExecutableProvider:
// the installed executable lives at the root of the installation
// directory. Callers stat it; the path is from this builder's own
// install logic, not user input.
func (p Platform) InstalledExecutable() (string, error) {
	return p.exePath(), nil
}

// InstalledVersion reads the ownership marker from the installed
// directory. Returns "" when there is no installation.
func (p Platform) InstalledVersion(context.Context) (string, error) {
	m, err := readMarker(p.DefaultInstallPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return m.Version, nil
}

// verifyStaged runs the staged executable with --version, requiring a
// clean exit and a version match before anything is activated. APPDATA
// is redirected to a throwaway directory so any first-run writes land
// in the sandbox instead of the user's real profile during
// verification.
func (p Platform) verifyStaged(ctx context.Context, staged, wantVersion string) error {
	executable := filepath.Join(staged, exeName)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	sandbox, err := os.MkdirTemp("", "aseprite-builder-verify-")
	if err != nil {
		return fmt.Errorf("create verification sandbox: %w", err)
	}
	defer func() { _ = os.RemoveAll(sandbox) }()

	res, err := p.Runner.Run(ctx, runner.Command{
		Name: executable,
		Args: []string{"--version"},
		Env:  []string{"APPDATA=" + sandbox},
	})
	if err != nil {
		return fmt.Errorf("run --version: %w", err)
	}
	if !res.OK() {
		return fmt.Errorf("--version exited with code %d", res.ExitCode)
	}
	stdout := strings.TrimSpace(res.Stdout)
	if !strings.Contains(stdout, wantVersion) {
		return fmt.Errorf("version check: got %q, want a string containing %q", stdout, wantVersion)
	}
	return nil
}

// markerPath is where the ownership marker lives inside the
// installation: next to the executable (non-bundle layout).
func markerPath(installDir string) string {
	return filepath.Join(installDir, markerName)
}

func writeMarker(installDir string, m marker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath(installDir), data, 0o644)
}

func readMarker(installDir string) (marker, error) {
	var m marker
	data, err := os.ReadFile(markerPath(installDir)) //nolint:gosec // path from trusted app logic
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse ownership marker: %w", err)
	}
	return m, nil
}

// copyDir recursively copies a directory tree preserving modes.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type %v in payload: %s", info.Mode(), path)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // path from trusted app logic
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(in, maxCopyBytes)); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

const maxCopyBytes = 6 << 30

// writeShortcut creates the Start Menu .lnk for the installed
// executable (idempotent: rewritten on each install).
func (p Platform) writeShortcut(target string) error {
	if err := os.MkdirAll(p.StartMenuRoot, 0o755); err != nil {
		return fmt.Errorf("create Start Menu dir: %w", err)
	}
	lnk := WriteShellLink(p.ShortcutPath(), ShortcutTarget{
		ExePath:     filepath.Join(target, exeName),
		WorkingDir:  target,
		IconPath:    filepath.Join(target, exeName),
		Description: "Aseprite (installed by aseprite-builder)",
	})
	return lnk
}
