package tui

// Pure model tests: Update/View transitions driven by synthetic
// messages, typed errors and key input — no terminal, no TTY (spec
// §37). The fakeService below scripts the app.Service surface so the
// full choice→service→events plumbing is exercised without
// infrastructure.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/akalp/aseprite-builder/internal/app"
	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/state"
)

// --- fake service ----------------------------------------------------------

// fakeService scripts service behavior and records calls.
type fakeService struct {
	// scripted outcomes
	installCalls  []app.InstallOptions
	installErr    error
	installOnce   bool // installErr applies to the first call only
	installRes    app.InstallResult
	installEvents []app.ProgressEvent

	updateErr    error
	rebuildErr   error
	repairErr    error
	blockInstall bool // Install blocks until its context is canceled

	uninstallCalls []app.UninstallOptions
	uninstallErr   error
	purgeCalls     []app.PurgeOptions
	purgeErr       error

	launchCalls int
	launchErr   error

	doctor    app.DoctorReport
	doctorErr error

	currentInstall state.InstallState
	hasInstall     bool

	latest    string
	latestErr error

	cfg   state.Config
	saves []state.Config
}

func (f *fakeService) Install(ctx context.Context, opts app.InstallOptions, sink app.ProgressSink) (app.InstallResult, error) {
	f.installCalls = append(f.installCalls, opts)
	err := f.installErr
	if f.installOnce {
		f.installErr = nil
	}
	for _, e := range f.installEvents {
		sink(e)
	}
	if f.blockInstall {
		<-ctx.Done()
		return app.InstallResult{}, ctx.Err()
	}
	return f.installRes, err
}

func (f *fakeService) Update(_ context.Context, opts app.InstallOptions, sink app.ProgressSink) (app.InstallResult, error) {
	f.installCalls = append(f.installCalls, opts)
	for _, e := range f.installEvents {
		sink(e)
	}
	return f.installRes, f.updateErr
}

func (f *fakeService) Rebuild(_ context.Context, opts app.RebuildOptions, sink app.ProgressSink) (app.InstallResult, error) {
	for _, e := range f.installEvents {
		sink(e)
	}
	return f.installRes, f.rebuildErr
}

func (f *fakeService) Repair(_ context.Context, sink app.ProgressSink) (app.InstallResult, error) {
	for _, e := range f.installEvents {
		sink(e)
	}
	return f.installRes, f.repairErr
}

func (f *fakeService) Launch(context.Context) error { f.launchCalls++; return f.launchErr }

func (f *fakeService) Uninstall(_ context.Context, opts app.UninstallOptions) error {
	f.uninstallCalls = append(f.uninstallCalls, opts)
	return f.uninstallErr
}

func (f *fakeService) PurgeCaches(opts app.PurgeOptions) []app.PurgeResult {
	f.purgeCalls = append(f.purgeCalls, opts)
	if f.purgeErr != nil {
		return []app.PurgeResult{{What: "sources", Err: f.purgeErr}}
	}
	return nil
}

func (f *fakeService) Doctor(context.Context) (app.DoctorReport, error) {
	return f.doctor, f.doctorErr
}

func (f *fakeService) CurrentInstall() (state.InstallState, bool, error) {
	return f.currentInstall, f.hasInstall, nil
}

func (f *fakeService) LatestStable(context.Context) (string, error) {
	return f.latest, f.latestErr
}

func (f *fakeService) Config() (state.Config, error) { return f.cfg, nil }

func (f *fakeService) SaveConfig(cfg state.Config) error {
	f.cfg = cfg
	f.saves = append(f.saves, cfg)
	return nil
}

func (f *fakeService) Layout() paths.Layout { return paths.Layout{} }

// --- harness ---------------------------------------------------------------

func newTestModel(t *testing.T, fake *fakeService) *Model {
	t.Helper()
	// Tests that don't care about the welcome flow start past it; the
	// acknowledgement is pre-seeded so no service save is triggered
	// (keeps save-call assertions clean).
	if !fake.cfg.NoticeAcknowledged {
		fake.cfg.NoticeAcknowledged = true
	}
	return newFirstRunModel(t, fake)
}

// newFirstRunModel is the raw first-run model: the persisted config
// decides whether the welcome screen shows.
func newFirstRunModel(t *testing.T, fake *fakeService) *Model {
	t.Helper()
	m := NewModel(context.Background(), fake, "test")
	drain(t, m, m.loadConfigCmd())
	drain(t, m, m.refreshHomeCmd())
	return m
}

// drain runs a command and feeds every produced message back into
// Update, following the chain while it stays synchronous. Commands
// backed by operation channels (waitForEvent) are followed as long as
// the fake completes instantly — which it does for every scripted
// test here; the cancellation test drives channels itself.
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	for i := 0; cmd != nil && i < 1000; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		_, next := m.Update(msg)
		cmd = next
	}
}

func send(t *testing.T, m *Model, msg tea.Msg) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(msg)
	return cmd
}

// press synthesizes a key press. Named keys map to their KeyType;
// anything else is a rune sequence.
func press(t *testing.T, m *Model, key string) tea.Cmd {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	_, cmd := m.Update(msg)
	return cmd
}

func viewContains(t *testing.T, m *Model, want ...string) {
	t.Helper()
	v := m.View()
	for _, w := range want {
		if !strings.Contains(v, w) {
			t.Errorf("view missing %q:\n%s", w, v)
		}
	}
}

// healthyDoctor is a systemcheck-ready report.
func healthyDoctor() app.DoctorReport {
	return app.DoctorReport{
		System: platform.SystemInfo{OS: "darwin", OSVersion: "15.2", Arch: "arm64", Supported: true},
		Requirements: []platform.Check{
			{Name: "Compiler", OK: true, Detail: "Apple Clang"},
			{Name: "Disk", OK: true},
		},
		Status: "healthy",
	}
}

// stagedEvents emits one event per pipeline stage plus compile ticks.
func stagedEvents() []app.ProgressEvent {
	return []app.ProgressEvent{
		{Stage: app.StageDetect, Message: "Checking operating system and architecture"},
		{Stage: app.StageSystemRequirements, Message: "Checking system-managed build requirements"},
		{Stage: app.StageResolveRelease, Message: "Resolving latest stable Aseprite release"},
		{Stage: app.StageFetchSource, Message: "Downloading official source archive"},
		{Stage: app.StageInspectContract, Message: "Inspecting upstream build contract"},
		{Stage: app.StageResolveTools, Message: "Resolving CMake and Ninja"},
		{Stage: app.StageResolveSkia, Message: "Discovering Skia requirement"},
		{Stage: app.StageBuild, Message: app.BuildPhaseConfigure},
		{Stage: app.StageBuild, Message: app.BuildPhaseCompile, Current: 25, Total: 100},
		{Stage: app.StageBuild, Message: app.BuildPhaseCompile, Current: 63, Total: 100},
		{Stage: app.StageVerify, Message: "Verifying"},
		{Stage: app.StageInstall, Message: "Installing Aseprite"},
	}
}

// installFlow drives home → systemcheck → installoptions → start.
func installFlow(t *testing.T, m *Model, fake *fakeService) tea.Cmd {
	t.Helper()
	// Home → Install Aseprite (first entry, not-installed home).
	if cmd := press(t, m, "enter"); cmd != nil {
		drain(t, m, cmd)
	}
	if m.screen != scrSystemCheck {
		t.Fatalf("screen = %v, want systemcheck", m.screen)
	}
	// systemcheck continue
	if cmd := press(t, m, "enter"); cmd != nil {
		drain(t, m, cmd)
	}
	if m.screen != scrInstallOptions {
		t.Fatalf("screen = %v, want installoptions", m.screen)
	}
	// down, down → "Start installation" (skips the two toggles)
	press(t, m, "down")
	press(t, m, "down")
	return press(t, m, "enter")
}

// --- welcome ----------------------------------------------------------------

func TestWelcomeShownOnceThenAcknowledged(t *testing.T) {
	fake := &fakeService{} // default config: notice not acknowledged
	m := newFirstRunModel(t, fake)
	if m.screen != scrWelcome {
		t.Fatalf("screen = %v, want welcome on first run", m.screen)
	}
	viewContains(t, m, "unofficial", "compiled on this machine")

	drain(t, m, press(t, m, "enter"))
	if m.screen != scrHome {
		t.Fatalf("screen = %v, want home after acknowledgement", m.screen)
	}
	if len(fake.saves) != 1 || !fake.saves[0].NoticeAcknowledged {
		t.Errorf("saves = %+v, want one acknowledged save", fake.saves)
	}
}

func TestWelcomeSkippedWhenAcknowledged(t *testing.T) {
	cfg := state.DefaultConfig()
	cfg.NoticeAcknowledged = true
	fake := &fakeService{cfg: cfg}
	m := newFirstRunModel(t, fake)
	if m.screen != scrHome {
		t.Fatalf("screen = %v, want home when notice acknowledged", m.screen)
	}
}

// --- home -------------------------------------------------------------------

func TestHomeNotInstalledView(t *testing.T) {
	fake := &fakeService{latest: "1.3.18.6"}
	m := newTestModel(t, fake)
	viewContains(t, m,
		"Aseprite is not installed.",
		"Latest stable: 1.3.18.6",
		"Install Aseprite", "Advanced", "Settings",
	)
}

func TestHomeInstalledViewAdaptsMenu(t *testing.T) {
	fake := &fakeService{
		latest:         "1.3.18.6",
		hasInstall:     true,
		currentInstall: state.InstallState{AsepriteVersion: "1.3.18.5", AppPath: "/fake/Aseprite.app"},
	}
	m := newTestModel(t, fake)
	viewContains(t, m,
		"Aseprite 1.3.18.5 installed.",
		"update available",
		"Launch Aseprite", "Update Aseprite",
	)
}

func TestHomeUpToDate(t *testing.T) {
	fake := &fakeService{
		latest:         "1.3.18.6",
		hasInstall:     true,
		currentInstall: state.InstallState{AsepriteVersion: "1.3.18.6", AppPath: "/fake/Aseprite.app"},
	}
	m := newTestModel(t, fake)
	viewContains(t, m, "up to date")
}

func TestHomeLatestUnknownOnNetworkFailure(t *testing.T) {
	fake := &fakeService{latestErr: errors.New("no such host")}
	m := newTestModel(t, fake)
	viewContains(t, m, "Latest stable: unknown (check your network)")
}

// --- install happy path -----------------------------------------------------

func TestInstallHappyPathFlow(t *testing.T) {
	fake := &fakeService{
		latest:        "1.3.18.6",
		doctor:        healthyDoctor(),
		installEvents: stagedEvents(),
		installRes:    app.InstallResult{Version: "1.3.18.6", AppPath: "/fake/Aseprite.app"},
	}
	m := newTestModel(t, fake)

	cmd := installFlow(t, m, fake)
	if !m.opRunning {
		t.Fatal("operation should be running after Start installation")
	}
	drain(t, m, cmd) // events + done + home refresh

	if m.opRunning {
		t.Error("operation should have finished")
	}
	if m.screen != scrOpResult {
		t.Fatalf("screen = %v, want op result", m.screen)
	}
	viewContains(t, m, "✅ Aseprite 1.3.18.6 installed: /fake/Aseprite.app")

	if len(fake.installCalls) != 1 {
		t.Fatalf("install calls = %d", len(fake.installCalls))
	}
	opts := fake.installCalls[0]
	if opts.Version != "" || opts.Channel != aseprite.ChannelStable {
		t.Errorf("opts = %+v", opts)
	}

	// Mid-flight rendering: one compile tick shows the percentage
	// (fresh row state, as mid-run).
	m.screen = scrInstalling
	m.resetRows()
	m.applyProgress(app.ProgressEvent{Stage: app.StageBuild, Message: app.BuildPhaseCompile, Current: 63, Total: 100})
	viewContains(t, m, "63%")
}

func TestInstallProgressRowsReflectStages(t *testing.T) {
	fake := &fakeService{
		latest:        "1.3.18.6",
		doctor:        healthyDoctor(),
		installEvents: stagedEvents(),
		installRes:    app.InstallResult{Version: "1.3.18.6", AppPath: "/fake/Aseprite.app"},
	}
	m := newTestModel(t, fake)
	drain(t, m, installFlow(t, m, fake))

	// Rows before the last active row completed; the install row stays
	// active because no later event marks it done (the result screen
	// takes over from there).
	for i := 0; i < nStageRows-1; i++ {
		if m.rows[i].state != rowDone {
			t.Errorf("row %d (%s) state = %v, want done", i, stageRowLabels[i], m.rows[i].state)
		}
	}
	if m.rows[nStageRows-1].state != rowActive {
		t.Errorf("install row state = %v, want active", m.rows[nStageRows-1].state)
	}
}

// --- failure paths ----------------------------------------------------------

func TestInstallRequirementsFailureShowsFixes(t *testing.T) {
	report := healthyDoctor()
	report.Requirements = []platform.Check{
		{Name: "Xcode", OK: false, Fix: "xcode-select --install"},
	}
	fake := &fakeService{doctor: report}
	m := newTestModel(t, fake)

	drain(t, m, press(t, m, "enter")) // home → install → systemcheck → doctor
	if m.screen != scrError {
		t.Fatalf("screen = %v, want error", m.screen)
	}
	viewContains(t, m, "missing some requirements", "xcode-select --install", "Nothing was installed or modified.")

	// Actions: Diagnostics + Back, no Retry (no operation was started).
	for _, a := range m.errSt.actions {
		if a.label == "Retry" {
			t.Error("retry must not be offered before an operation exists")
		}
	}
	drain(t, m, press(t, m, "enter")) // first action = Diagnostics
	if m.screen != scrDiagnostics {
		t.Fatalf("screen = %v, want diagnostics after action", m.screen)
	}
}

func TestBuildFailureErrorScreenAndRetry(t *testing.T) {
	fake := &fakeService{
		latest:      "1.3.18.6",
		doctor:      healthyDoctor(),
		installErr:  &aseprite.UpstreamBuildError{ExitCode: 2, Tail: "ninja: build stopped"},
		installOnce: true,
		installRes:  app.InstallResult{LogPath: "/fake/logs/install.log"},
	}
	m := newTestModel(t, fake)
	drain(t, m, installFlow(t, m, fake))

	if m.screen != scrError {
		t.Fatalf("screen = %v, want error", m.screen)
	}
	viewContains(t, m,
		"could not be compiled",
		"Full details: /fake/logs/install.log",
	)
	labels := errActionLabels(m.errSt.actions)
	for _, want := range []string{"View log", "Diagnostics", "Retry", "Back"} {
		if !contains(labels, want) {
			t.Errorf("actions = %v, missing %q", labels, want)
		}
	}

	// Retry re-runs the same operation (which now succeeds).
	drain(t, m, press(t, m, "down")) // down: View log → Diagnostics → Retry
	// (two more downs to reach Retry: View log, Diagnostics, Retry)
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if len(fake.installCalls) != 2 {
		t.Errorf("install calls = %d, want 2 after retry", len(fake.installCalls))
	}
}

func TestViewLogShowsTail(t *testing.T) {
	logFile := writeTempLog(t, 60, "ninja: build stopped: subcommand failed")

	fake := &fakeService{
		latest:     "1.3.18.6",
		doctor:     healthyDoctor(),
		installErr: errors.New("boom"),
		installRes: app.InstallResult{LogPath: logFile},
	}
	m := newTestModel(t, fake)
	drain(t, m, installFlow(t, m, fake))

	// First action is View log.
	drain(t, m, press(t, m, "enter"))
	if !m.errSt.showDetail {
		t.Fatal("View log should reveal the tail")
	}
	viewContains(t, m, "ninja: build stopped: subcommand failed")
	// Only the tail is kept.
	if got := strings.Count(m.errSt.detail, "\n") + 1; got != 40 {
		t.Errorf("tail lines = %d, want 40", got)
	}
}

func TestUnknownContractDistinctScreen(t *testing.T) {
	fake := &fakeService{
		latest:     "1.3.18.6",
		doctor:     healthyDoctor(),
		installErr: app.UnknownContractError{Reason: errors.New("missing build.sh")},
		installRes: app.InstallResult{LogPath: "/fake/logs/install.log"},
	}
	m := newTestModel(t, fake)
	drain(t, m, installFlow(t, m, fake))

	if m.screen != scrError {
		t.Fatalf("screen = %v, want error", m.screen)
	}
	viewContains(t, m, "does not recognize")
	labels := errActionLabels(m.errSt.actions)
	for _, want := range []string{"Use installed version", "Diagnostics", "Exit"} {
		if !contains(labels, want) {
			t.Errorf("contract actions = %v, missing %q", labels, want)
		}
	}
	if contains(labels, "Retry") {
		t.Error("retry must not be offered for unknown contracts (fail safely)")
	}
}

func TestUpdateUpToDateIsInfoNotError(t *testing.T) {
	fake := &fakeService{
		latest:     "1.3.18.6",
		hasInstall: true,
		currentInstall: state.InstallState{
			AsepriteVersion: "1.3.18.6", AppPath: "/fake/Aseprite.app",
		},
		updateErr: app.ErrUpToDate,
	}
	m := newTestModel(t, fake)

	// Home menu (installed): idx1 = Update Aseprite.
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if m.screen != scrConfirmation {
		t.Fatalf("screen = %v, want confirmation for update", m.screen)
	}
	// Confirm (last entries are Confirm/Cancel).
	drain(t, m, press(t, m, "enter"))
	if m.screen != scrHome {
		t.Fatalf("screen = %v, want home after up-to-date update", m.screen)
	}
	viewContains(t, m, "already up to date")
}

// --- cancellation -----------------------------------------------------------

func TestCtrlCCancelsRunningOperation(t *testing.T) {
	fake := &fakeService{
		latest:       "1.3.18.6",
		doctor:       healthyDoctor(),
		blockInstall: true,
	}
	m := newTestModel(t, fake)
	_ = installFlow(t, m, fake) // op starts; fake blocks on ctx.Done()

	// First Ctrl+C cancels the op, does not quit the model.
	if c := press(t, m, "ctrl+c"); c != nil {
		t.Error("first ctrl+c must not quit")
	}
	if m.opCancel == nil {
		t.Fatal("opCancel must be set")
	}

	// The fake now unblocks and returns context.Canceled → error screen.
	settleUntil(t, m, m.opEvents, m.opDone, func() bool { return m.screen == scrError }, 5*time.Second)
	if !strings.Contains(app.FriendlyMessage(m.errSt.err), "cancelled") {
		t.Errorf("message = %q", app.FriendlyMessage(m.errSt.err))
	}
	labels := errActionLabels(m.errSt.actions)
	if !contains(labels, "Retry") || !contains(labels, "Back") {
		t.Errorf("actions = %v", labels)
	}

	// Second Ctrl+C with nothing running quits.
	if c := press(t, m, "ctrl+c"); c == nil {
		t.Error("ctrl+c with no running op must quit")
	}
}

// settleUntil drives the op channels until cond holds or timeout.
func settleUntil(t *testing.T, m *Model, events, done <-chan tea.Msg, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		msg := waitForEvent(events, done)()
		_, next := m.Update(msg)
		if next == nil && !m.opRunning {
			// Operation finished; stop feeding stale buffered events.
			if cond() {
				return
			}
			continue
		}
	}
	t.Fatal("timed out waiting for condition")
}

// --- settings ---------------------------------------------------------------

func TestSettingsToggleSavesConfig(t *testing.T) {
	fake := &fakeService{cfg: state.DefaultConfig()}
	m := newTestModel(t, fake)

	// Not-installed home: Install, Advanced, Settings, Diagnostics, Exit.
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if m.screen != scrSettings {
		t.Fatalf("screen = %v, want settings", m.screen)
	}

	// Toggle keep-workspace (idx 0) — the user stays in settings.
	drain(t, m, press(t, m, "enter"))
	if !fake.cfg.KeepWorkspace {
		t.Errorf("persisted cfg = %+v, want KeepWorkspace", fake.cfg)
	}
	if m.screen != scrSettings {
		t.Errorf("screen = %v, want settings after save", m.screen)
	}

	// Toggle verbose (idx 1).
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if !fake.cfg.Verbose {
		t.Errorf("persisted cfg = %+v, want Verbose", fake.cfg)
	}

	if len(fake.saves) != 2 {
		t.Fatalf("saves = %d, want 2", len(fake.saves))
	}
	viewContains(t, m, "[x] Keep build workspace", "[x] Verbose operation logs")
}

func TestSettingsSaveFailureIsSafe(t *testing.T) {
	// The settings save path goes through Service.SaveConfig; the TUI
	// surfaces errors on the standard error screen. Covered via
	// savedMsg handling above; here just ensure the flow ends sane
	// when a save fails.
	fake := &fakeService{cfg: state.DefaultConfig()}
	m := newTestModel(t, fake)
	send(t, m, savedMsg{err: errors.New("disk full")})
	if m.screen != scrError {
		t.Fatalf("screen = %v, want error on failed save", m.screen)
	}
	viewContains(t, m, "disk full")
}

// --- advanced ---------------------------------------------------------------

func TestAdvancedPinnedVersionInstall(t *testing.T) {
	fake := &fakeService{
		latest:        "1.3.18.6",
		doctor:        healthyDoctor(),
		installEvents: stagedEvents(),
		installRes:    app.InstallResult{Version: "1.3.10", AppPath: "/fake/Aseprite.app"},
	}
	m := newTestModel(t, fake)

	// Home → Advanced (idx1) → Install a specific version (idx0).
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if m.screen != scrAdvanced {
		t.Fatalf("screen = %v, want advanced", m.screen)
	}
	drain(t, m, press(t, m, "enter")) // first item opens the input
	if !m.pinActive {
		t.Fatal("pin input should be active")
	}

	// Type a version.
	send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1.3.10")})
	if m.pinned != "1.3.10" {
		t.Fatalf("pinned = %q", m.pinned)
	}
	// Backspace once then retype the last char to exercise editing.
	press(t, m, "backspace")
	send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0")})

	drain(t, m, press(t, m, "enter")) // commit → systemcheck → doctor
	if m.screen != scrSystemCheck {
		t.Fatalf("screen = %v, want systemcheck after pin commit", m.screen)
	}
	drain(t, m, press(t, m, "enter")) // continue
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter")) // start

	if len(fake.installCalls) != 1 {
		t.Fatalf("install calls = %d", len(fake.installCalls))
	}
	if got := fake.installCalls[0].Version; got != "1.3.10" {
		t.Errorf("pinned version = %q, want 1.3.10", got)
	}
}

func TestUninstallConfirmWithPurgeScopes(t *testing.T) {
	fake := &fakeService{
		latest:         "1.3.18.6",
		hasInstall:     true,
		currentInstall: state.InstallState{AsepriteVersion: "1.3.18.6", AppPath: "/fake/Aseprite.app", PreviousAppPath: "/fake/prev.app"},
	}
	m := newTestModel(t, fake)

	// Home → Advanced (idx3 in the installed menu) → Uninstall (idx3).
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter")) // Advanced
	if m.screen != scrAdvanced || m.advIdx != 0 {
		t.Fatalf("advanced nav broken: screen=%v idx=%d", m.screen, m.advIdx)
	}
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter")) // Uninstall Aseprite…
	if m.screen != scrConfirmation {
		t.Fatalf("screen = %v, want uninstall confirmation", m.screen)
	}

	// idx0: previous version toggle (present because PreviousAppPath set).
	drain(t, m, press(t, m, "enter"))
	// idx2: tool cache purge toggle.
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	// Now navigate to Confirm (entries: prev, sources, tools, skia, logs, Confirm, Cancel).
	for i := 0; i < 3; i++ {
		press(t, m, "down")
	}
	drain(t, m, press(t, m, "enter"))

	if len(fake.uninstallCalls) != 1 || !fake.uninstallCalls[0].RemovePrevious {
		t.Errorf("uninstall calls = %+v", fake.uninstallCalls)
	}
	if len(fake.purgeCalls) != 1 {
		t.Fatalf("purge calls = %+v", fake.purgeCalls)
	}
	p := fake.purgeCalls[0]
	if !p.Tools || p.Sources || p.Skia || p.Logs {
		t.Errorf("purge = %+v, want tools only", p)
	}
	if m.screen != scrOpResult {
		t.Fatalf("screen = %v, want result", m.screen)
	}
	viewContains(t, m, "Removed the application.")
}

// --- diagnostics ------------------------------------------------------------

func TestDiagnosticsScreenShowsDoctorFacts(t *testing.T) {
	fake := &fakeService{doctor: healthyDoctor()}
	m := newTestModel(t, fake)

	// Not-installed home menu idx3 = Diagnostics.
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "down")
	drain(t, m, press(t, m, "enter"))
	if m.screen != scrDiagnostics {
		t.Fatalf("screen = %v, want diagnostics", m.screen)
	}
	viewContains(t, m, "Diagnostics", "✓ Operating system", "darwin 15.2", "Everything looks good.")
}

// --- helpers ----------------------------------------------------------------

func errActionLabels(actions []errAction) []string {
	var out []string
	for _, a := range actions {
		out = append(out, a.label)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func writeTempLog(t *testing.T, lines int, last string) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	b.WriteString(last + "\n")
	path := t.TempDir() + "/install.log"
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
