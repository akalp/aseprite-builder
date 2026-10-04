package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/akalp/aseprite-builder/internal/app"
	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/state"
)

func TestSettingsReleaseChannelToggle(t *testing.T) {
	fake := &fakeService{cfg: state.DefaultConfig()}
	m := newTestModel(t, fake)

	// Not-installed home: Install, Advanced, Settings, Diagnostics, Exit.
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if m.screen != scrSettings {
		t.Fatalf("screen = %v, want settings", m.screen)
	}

	// Release channel is the third settings item (after the two toggles).
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if fake.cfg.Channel != string(aseprite.ChannelDev) {
		t.Fatalf("persisted channel = %q, want dev", fake.cfg.Channel)
	}
	viewContains(t, m, "Release channel: dev")

	// Toggling again returns to stable.
	drain(t, m, press(t, m, "enter"))
	if fake.cfg.Channel != string(aseprite.ChannelStable) {
		t.Fatalf("persisted channel = %q, want stable", fake.cfg.Channel)
	}
	viewContains(t, m, "Release channel: stable")
}

func TestDevChannelInstallPromptsForCommit(t *testing.T) {
	fake := &fakeService{
		cfg:           state.Config{Channel: string(aseprite.ChannelDev), NoticeAcknowledged: true},
		latest:        "1.3.18.6",
		doctor:        healthyDoctor(),
		installEvents: stagedEvents(),
		installRes:    app.InstallResult{Version: "dev-abc1234", AppPath: "/fake/Aseprite.app"},
	}
	m := newTestModel(t, fake)

	// "Install Aseprite" opens the commit prompt instead of jumping on.
	drain(t, m, press(t, m, "enter"))
	if !m.devActive || m.screen != scrHome {
		t.Fatalf("devActive=%v screen=%v, want the home commit prompt", m.devActive, m.screen)
	}
	viewContains(t, m, "Commit to install")

	// Type, edit, commit.
	send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc1234def")})
	press(t, m, "backspace")
	drain(t, m, press(t, m, "enter"))
	if m.devActive {
		t.Error("prompt must close after committing")
	}
	if m.screen != scrSystemCheck {
		t.Fatalf("screen = %v, want systemcheck after the commit", m.screen)
	}

	drain(t, m, press(t, m, "enter")) // systemcheck continue
	viewContains(t, m, "development commit abc1234")
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter")) // start

	if len(fake.installCalls) != 1 {
		t.Fatalf("install calls = %d, want 1", len(fake.installCalls))
	}
	got := fake.installCalls[0]
	if got.Channel != aseprite.ChannelDev || got.Commit != "abc1234de" {
		t.Errorf("install options = %+v, want dev channel + commit abc1234de", got)
	}
}

func TestDevChannelPromptEscapeCancels(t *testing.T) {
	fake := &fakeService{
		cfg:    state.Config{Channel: string(aseprite.ChannelDev), NoticeAcknowledged: true},
		latest: "1.3.18.6",
	}
	m := newTestModel(t, fake)

	drain(t, m, press(t, m, "enter"))
	if !m.devActive {
		t.Fatal("prompt should be active")
	}
	press(t, m, "esc")
	if m.devActive || m.screen != scrHome {
		t.Fatalf("devActive=%v screen=%v, want home without the prompt", m.devActive, m.screen)
	}
	if len(fake.installCalls) != 0 {
		t.Errorf("no install may start after esc: %+v", fake.installCalls)
	}
}

func TestStableChannelInstallSkipsPrompt(t *testing.T) {
	fake := &fakeService{
		cfg:           state.DefaultConfig(),
		latest:        "1.3.18.6",
		doctor:        healthyDoctor(),
		installEvents: stagedEvents(),
	}
	m := newTestModel(t, fake)
	drain(t, m, press(t, m, "enter"))
	if m.devActive {
		t.Fatal("stable channel must not prompt for a commit")
	}
	if m.screen != scrSystemCheck {
		t.Fatalf("screen = %v, want systemcheck", m.screen)
	}
}
