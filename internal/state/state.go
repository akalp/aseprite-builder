// Package state persists aseprite-builder configuration and
// installation metadata as JSON under the user's data directory.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// InstallState records one aseprite-builder-managed Aseprite
// installation. It is the source of truth for update/repair/uninstall
// decisions, so it must contain everything needed to act safely on the
// installed bundle.
type InstallState struct {
	// AsepriteVersion is the installed Aseprite version ("1.3.18.6").
	AsepriteVersion string `json:"aseprite_version"`
	// TagName is the upstream release tag ("v1.3.18.6").
	TagName string `json:"tag_name"`
	// Channel records which channel was installed ("stable" or "dev").
	Channel string `json:"channel"`
	// CommitSHA is the explicit development-channel commit for dev
	// installs ("" for stable releases).
	CommitSHA string `json:"commit_sha,omitempty"`
	// SourceURL is where the source archive came from.
	SourceURL string `json:"source_url"`
	// SourceSHA256 is the verified source archive digest (may be empty
	// when upstream did not publish one).
	SourceSHA256 string `json:"source_sha256,omitempty"`
	// SkiaTag is the upstream Skia tag used for the build.
	SkiaTag string `json:"skia_tag,omitempty"`
	// BuildContractVersion summarizes the detected upstream contract so
	// future builder releases can detect contract drift.
	BuildContract string `json:"build_contract,omitempty"`
	// AppPath is the installed application path (Aseprite.app).
	AppPath string `json:"app_path"`
	// InstalledAt is when the bundle was activated.
	InstalledAt time.Time `json:"installed_at"`
	// PreviousAppPath optionally retains the previous installation for
	// rollback, kept until the next update succeeds.
	PreviousAppPath string `json:"previous_app_path,omitempty"`
	// WorkspacePath is the build workspace directory for this install.
	WorkspacePath string `json:"workspace_path"`
}

// Config holds user preferences. Defaults are conservative; only a few
// options exist so the tool stays simple.
type Config struct {
	// Channel for new installs: "stable" (default) or "dev".
	Channel string `json:"channel"`
	// KeepWorkspace retains build workspaces after a successful install
	// (useful for repair/rebuild); false cleans heavy artifacts.
	KeepWorkspace bool `json:"keep_workspace"`
	// Verbose enables chatty logging on stderr.
	Verbose bool `json:"verbose"`
	// NoticeAcknowledged records that the user dismissed the §7
	// licensing notice, so the TUI welcome screen is shown once.
	NoticeAcknowledged bool `json:"notice_acknowledged,omitempty"`
	// InstallDir overrides the platform's default installation root
	// ("" uses the platform default).
	InstallDir string `json:"install_dir,omitempty"`
	// KeepSources retains older source trees instead of pruning them to
	// the active and previous generation. The active tree is always
	// kept for repair/rebuild.
	KeepSources bool `json:"keep_sources,omitempty"`
	// ToolPreference selects which reuse source wins before downloading:
	// "system" (default) prefers a compatible system tool; "cached"
	// prefers the builder's own downloaded cache.
	ToolPreference string `json:"tool_preference,omitempty"`
	// RetainBuilds keeps the replaced installation after updates for
	// rollback (spec §24). Default true; no omitempty so an explicit
	// false persists.
	RetainBuilds bool `json:"retain_builds"`
}

// DefaultConfig returns the zero-configuration defaults.
func DefaultConfig() Config {
	return Config{Channel: "stable", KeepWorkspace: false, Verbose: false, ToolPreference: "system", RetainBuilds: true}
}

// Store persists state and config JSON files in dir. It never overwrites
// install state destructively: SaveInstall writes a fresh file and
// rotates the old one, so a crash mid-save leaves the previous state
// readable.
type Store struct {
	// Dir is the state directory (paths.Layout.State).
	Dir string
}

// ErrNoState is returned when no installation exists yet.
var ErrNoState = errors.New("no aseprite-builder installation recorded")

// ConfigPath is the JSON file holding user config.
func (s Store) ConfigPath() string { return filepath.Join(s.Dir, "config.json") }

// InstallPath is the JSON file holding the current installation record.
func (s Store) InstallPath() string { return filepath.Join(s.Dir, "install.json") }

// LoadConfig reads config.json, returning defaults when absent.
func (s Store) LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(s.ConfigPath()) //nolint:gosec // path from trusted app logic
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", s.ConfigPath(), err)
	}
	return cfg, nil
}

// SaveConfig writes config.json atomically.
func (s Store) SaveConfig(cfg Config) error {
	return s.writeJSON(s.ConfigPath(), cfg)
}

// LoadInstall reads install.json.
func (s Store) LoadInstall() (InstallState, error) {
	data, err := os.ReadFile(s.InstallPath()) //nolint:gosec // path from trusted app logic
	if errors.Is(err, os.ErrNotExist) {
		return InstallState{}, ErrNoState
	}
	if err != nil {
		return InstallState{}, err
	}
	var st InstallState
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parse install state %s: %w", s.InstallPath(), err)
	}
	if st.AsepriteVersion == "" || st.AppPath == "" {
		return st, fmt.Errorf("install state %s is incomplete: missing version or app path", s.InstallPath())
	}
	return st, nil
}

// SaveInstall writes install.json atomically. The write is
// write-temp-then-rename, so interrupted saves never corrupt the file.
func (s Store) SaveInstall(st InstallState) error {
	if st.AsepriteVersion == "" || st.AppPath == "" {
		return errors.New("refusing to save incomplete install state")
	}
	return s.writeJSON(s.InstallPath(), st)
}

// ClearInstall removes install.json (after a successful uninstall).
func (s Store) ClearInstall() error {
	err := os.Remove(s.InstallPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s Store) writeJSON(path string, v any) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write state temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit state file: %w", err)
	}
	return nil
}
