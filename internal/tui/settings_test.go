package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/akalp/aseprite-builder/internal/state"
)

// openSettings drives home → Settings for the not-installed menu.
func openSettings(t *testing.T, m *Model) {
	t.Helper()
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if m.screen != scrSettings {
		t.Fatalf("screen = %v, want settings", m.screen)
	}
}

func TestSettingsAdvancedToggles(t *testing.T) {
	fake := &fakeService{cfg: state.DefaultConfig()}
	m := newTestModel(t, fake)
	openSettings(t, m)

	// Items: 0 keep-workspace, 1 verbose, 2 channel, 3 install-dir,
	// 4 keep-sources, 5 retain-builds, 6 cached-tools, 7 back.
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter")) // keep old source trees
	if !fake.cfg.KeepSources {
		t.Errorf("cfg = %+v, want KeepSources", fake.cfg)
	}

	press(t, m, "down")
	drain(t, m, press(t, m, "enter")) // keep previous build (default true → false)
	if fake.cfg.RetainBuilds {
		t.Errorf("cfg = %+v, want RetainBuilds=false", fake.cfg)
	}

	press(t, m, "down")
	drain(t, m, press(t, m, "enter")) // prefer cached tools
	if fake.cfg.ToolPreference != "cached" {
		t.Errorf("cfg = %+v, want cached tool preference", fake.cfg)
	}
	viewContains(t, m, "[x] Keep old source trees", "[ ] Keep previous build (rollback)", "[x] Prefer cached build tools")
}

func TestSettingsInstallDirEntryValidatesAbsolutePaths(t *testing.T) {
	fake := &fakeService{cfg: state.DefaultConfig()}
	m := newTestModel(t, fake)
	openSettings(t, m)

	// Item 3 opens the install-directory text entry.
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if !m.cfgEdit {
		t.Fatal("install-dir entry should be active")
	}
	viewContains(t, m, "Install directory (absolute path")

	// A relative path is rejected locally (nothing saved).
	send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("relative/path")})
	drain(t, m, press(t, m, "enter"))
	if !m.cfgEdit || m.cfgEditErr == "" {
		t.Fatalf("relative path must stay in the editor with a hint: edit=%v err=%q", m.cfgEdit, m.cfgEditErr)
	}
	if fake.cfg.InstallDir != "" {
		t.Fatalf("nothing may be saved for an invalid path: %+v", fake.cfg)
	}

	// An absolute path saves; esc would cancel.
	custom := filepath.Join(t.TempDir(), "custom apps")
	m.cfgEditVal = custom
	drain(t, m, press(t, m, "enter"))
	if m.cfgEdit {
		t.Error("editor must close after a valid save")
	}
	if fake.cfg.InstallDir != custom {
		t.Errorf("saved InstallDir = %q, want %q", fake.cfg.InstallDir, custom)
	}
	viewContains(t, m, "Install directory: "+custom)
}

func TestSettingsInstallDirEntryEscapeCancels(t *testing.T) {
	fake := &fakeService{cfg: state.DefaultConfig()}
	m := newTestModel(t, fake)
	openSettings(t, m)
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/tmp/x")})

	press(t, m, "esc")
	if m.cfgEdit {
		t.Error("esc must close the editor")
	}
	if fake.cfg.InstallDir != "" {
		t.Errorf("esc must not save: %+v", fake.cfg)
	}
}
