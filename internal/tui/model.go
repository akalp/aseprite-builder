// Package tui is the interactive primary interface (spec §12, §34).
// It contains no installer logic: every screen translates user
// choices into calls on the shared application services
// (internal/app) and renders app.ProgressEvent streams. This file
// holds the state machine — Model, Update and the navigation graph;
// rendering lives in screens.go, plumbing in events.go.
package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/akalp/aseprite-builder/internal/app"
	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/state"
)

// Service is the slice of the application service layer the TUI
// drives. *app.Service satisfies it; tests provide a scripted fake.
// Keeping it as an interface keeps Bubble Tea out of internal/app
// (spec §34) and the model free of installer logic (spec §42).
type Service interface {
	Install(ctx context.Context, opts app.InstallOptions, sink app.ProgressSink) (app.InstallResult, error)
	Update(ctx context.Context, opts app.InstallOptions, sink app.ProgressSink) (app.InstallResult, error)
	Rebuild(ctx context.Context, opts app.RebuildOptions, sink app.ProgressSink) (app.InstallResult, error)
	Repair(ctx context.Context, sink app.ProgressSink) (app.InstallResult, error)
	Launch(ctx context.Context) error
	Uninstall(ctx context.Context, opts app.UninstallOptions) error
	PurgeCaches(opts app.PurgeOptions) []app.PurgeResult
	Doctor(ctx context.Context) (app.DoctorReport, error)
	CurrentInstall() (state.InstallState, bool, error)
	LatestStable(ctx context.Context) (string, error)
	Config() (state.Config, error)
	SaveConfig(cfg state.Config) error
	Layout() paths.Layout
}

// screen is one logical TUI screen (spec §34). UpdateAvailable and
// Updating collapse into Home and Installing respectively: the home
// banner announces an update, and Installing renders an update label
// for update operations.
type screen int

const (
	scrWelcome screen = iota
	scrHome
	scrSystemCheck
	scrInstallOptions
	scrInstalling
	scrOpResult
	scrDiagnostics
	scrSettings
	scrAdvanced
	scrConfirmation
	scrError
)

// opKind is the operation currently selected, running, or finished.
type opKind int

const (
	opNone opKind = iota
	opInstall
	opUpdate
	opRebuild
	opRepair
	opUninstall
)

// retryable reports whether the error screen may offer Retry for an
// operation kind (launch has no progress plumbing and is never
// retried through this path).
func (k opKind) retryable() bool {
	switch k {
	case opInstall, opUpdate, opRebuild, opRepair, opUninstall:
		return true
	default:
		return false
	}
}

// menuItem is one selectable row of a menu screen.
type menuItem struct {
	label string
	run   func(m *Model) tea.Cmd
}

// stageRow is one row of the install progress table.
type stageRow struct {
	state      rowState
	annotation string // percent-ish right column of the active row
}

type rowState int

const (
	rowPending rowState = iota
	rowActive
	rowDone
)

// confirmEntry is one row of a confirmation screen: either a toggle
// (space flips it) or an action (enter runs it).
type confirmEntry struct {
	label  string
	toggle *bool
	run    func(m *Model) tea.Cmd
}

type confirmState struct {
	title   string
	body    string
	entries []confirmEntry
	idx     int
	back    screen
}

// errAction is one action offered on an error screen.
type errAction struct {
	label string
	run   func(m *Model) tea.Cmd
}

type errorState struct {
	err        error
	logPath    string
	detail     string // log tail shown by "View log"
	showDetail bool
	actions    []errAction
	idx        int
}

type resultState struct {
	kind   opKind
	result app.InstallResult
	purge  []app.PurgeResult
}

// Model is the TUI state machine root.
type Model struct {
	svc            Service
	builderVersion string
	baseCtx        context.Context

	screen screen
	width  int
	height int

	// Home data.
	install    state.InstallState
	hasInstall bool
	homeLoaded bool
	homeErr    error
	homeNotice string

	latest        string
	latestErr     error
	latestLoading bool

	homeItems []menuItem
	homeIdx   int

	// Operation plumbing.
	req       opRequest
	opCancel  context.CancelFunc
	opEvents  <-chan tea.Msg
	opDone    <-chan tea.Msg
	opRunning bool
	opKill    bool // first Ctrl+C seen while an op runs

	rows [nStageRows]stageRow

	// Diagnostics.
	doctor        app.DoctorReport
	doctorErr     error
	doctorLoading bool

	// Settings.
	cfg        state.Config
	cfgLoading bool
	cfgItems   []menuItem
	cfgIdx     int

	// Advanced.
	advIdx    int
	advItems  []menuItem
	pinned    string
	pinActive bool

	confirm confirmState
	errSt   errorState
	opRes   resultState

	// installoptions toggles (persisted config independent).
	optLaunch bool
	optForce  bool

	// Post-exit summary printed to stderr after the program returns,
	// so the durable facts survive in scrollback.
	exitSummary []string
}

// NewModel returns the initial TUI model.
func NewModel(ctx context.Context, svc Service, builderVersion string) *Model {
	m := &Model{
		svc:            svc,
		builderVersion: builderVersion,
		baseCtx:        ctx,
		screen:         scrHome,
	}
	return m
}

// Init loads the persisted config (which may route through the
// welcome screen) and the home data.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadConfigCmd(), m.refreshHomeCmd())
}

// --- commands -------------------------------------------------------------

func (m *Model) refreshHomeCmd() tea.Cmd {
	return func() tea.Msg {
		install, has, err := m.svc.CurrentInstall()
		return homeDataMsg{install: install, has: has, err: err}
	}
}

func (m *Model) fetchLatestCmd() tea.Cmd {
	return func() tea.Msg {
		v, err := m.svc.LatestStable(m.baseCtx)
		return latestMsg{v: v, err: err}
	}
}

func (m *Model) loadConfigCmd() tea.Cmd {
	return func() tea.Msg {
		cfg, err := m.svc.Config()
		return cfgMsg{cfg: cfg, err: err}
	}
}

func (m *Model) runDoctorCmd() tea.Cmd {
	return func() tea.Msg {
		report, err := m.svc.Doctor(m.baseCtx)
		return doctorMsg{report: report, err: err}
	}
}

func (m *Model) launchCmd() tea.Cmd {
	return func() tea.Msg {
		return launchDoneMsg{err: m.svc.Launch(m.baseCtx)}
	}
}

// --- messages -------------------------------------------------------------

type homeDataMsg struct {
	install state.InstallState
	has     bool
	err     error
}

type latestMsg struct {
	v   string
	err error
}

type cfgMsg struct {
	cfg state.Config
	err error
}

type doctorMsg struct {
	report app.DoctorReport
	err    error
}

type launchDoneMsg struct{ err error }

// --- update ---------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case progressMsg:
		m.applyProgress(msg.e)
		return m, waitForEvent(m.opEvents, m.opDone)

	case opDoneMsg:
		// All event sends precede the done send in the op goroutine,
		// so drain remaining buffered events first to keep the
		// progress table complete before routing the outcome.
		for {
			select {
			case e := <-m.opEvents:
				if pm, ok := e.(progressMsg); ok {
					m.applyProgress(pm.e)
				}
			default:
				return m, m.finishOp(msg)
			}
		}

	case homeDataMsg:
		m.homeLoaded = true
		m.homeErr = msg.err
		m.install, m.hasInstall = msg.install, msg.has
		m.homeItems = m.buildHomeItems()
		m.homeIdx = 0
		// Refresh the latest-stable banner every time home data loads
		// (a successful install changes what "up to date" means).
		m.latestLoading = true
		return m, m.fetchLatestCmd()

	case latestMsg:
		m.latestLoading = false
		if msg.err != nil {
			m.latestErr = msg.err
			m.latest = ""
		} else {
			m.latestErr = nil
			m.latest = msg.v
		}
		return m, nil

	case cfgMsg:
		m.cfgLoading = false
		if msg.err == nil {
			m.cfg = msg.cfg
			m.cfgItems = m.buildSettingsItems()
			if !m.cfg.NoticeAcknowledged && m.screen == scrHome && !m.opRunning {
				m.screen = scrWelcome
			}
		}
		return m, nil

	case savedMsg:
		if msg.err != nil {
			m.goToError(msg.err, "", opNone)
			return m, nil
		}
		// A settings save keeps the user on the settings screen (the
		// toggle state itself is the feedback); other saves land home.
		if m.screen != scrSettings {
			m.screen = scrHome
			m.homeNotice = "Settings saved."
		}
		return m, nil

	case doctorMsg:
		m.doctorLoading = false
		if msg.err != nil {
			m.goToError(msg.err, "", opNone)
			return m, nil
		}
		m.doctor = msg.report
		if m.screen == scrSystemCheck {
			// §43.4: missing system-managed prerequisites are a clear
			// message before anything else happens.
			for _, c := range msg.report.Requirements {
				if !c.OK {
					m.goToError(app.SystemRequirementsError{Checks: msg.report.Requirements}, "", opNone)
					return m, nil
				}
			}
		}
		return m, nil

	case launchDoneMsg:
		if msg.err != nil {
			m.goToError(msg.err, "", opNone)
			return m, nil
		}
		m.screen = scrHome
		m.homeNotice = "Aseprite launched."
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global: Ctrl+C always cancels the running op first, quits on the
	// second press (or immediately when nothing runs).
	if key == "ctrl+c" {
		if m.opRunning {
			if m.opKill { // second press: force quit (op ctx stays canceled)
				return m, tea.Quit
			}
			m.opKill = true
			if m.opCancel != nil {
				m.opCancel()
			}
			return m, nil
		}
		return m, tea.Quit
	}

	switch m.screen {
	case scrWelcome:
		if key == "q" {
			return m, tea.Quit
		}
		return m, m.acknowledgeNotice()

	case scrHome:
		switch key {
		case "up", "k":
			if m.homeIdx > 0 {
				m.homeIdx--
			}
		case "down", "j":
			if m.homeIdx < len(m.homeItems)-1 {
				m.homeIdx++
			}
		case "enter":
			if m.homeIdx < len(m.homeItems) {
				return m, m.homeItems[m.homeIdx].run(m)
			}
		case "q", "esc":
			return m, tea.Quit
		default:
			m.homeNotice = ""
		}

	case scrSystemCheck:
		switch key {
		case "enter":
			if !m.doctorLoading && m.doctorErr == nil {
				m.homeIdx = 0
				m.screen = scrInstallOptions
			}
		case "esc", "q":
			m.screen = scrHome
			return m, m.refreshHomeCmd()
		}

	case scrInstallOptions:
		items := m.installOptionItems()
		switch key {
		case "up", "k":
			if m.homeIdx > 0 {
				m.homeIdx--
			}
		case "down", "j":
			if m.homeIdx < len(items)-1 {
				m.homeIdx++
			}
		case "enter", " ":
			if m.homeIdx < len(items) {
				return m, items[m.homeIdx].run(m)
			}
		case "esc", "q":
			m.screen = scrHome
			return m, m.refreshHomeCmd()
		}

	case scrInstalling:
		// The only key that matters here is handled globally above
		// (Ctrl+C cancels the op; the done message moves us on).

	case scrOpResult:
		switch key {
		case "enter", "esc", "q":
			m.screen = scrHome
			m.homeNotice = ""
			return m, m.refreshHomeCmd()
		case "l":
			if m.opRes.kind == opInstall && m.opRes.result.AppPath != "" {
				return m, m.launchCmd()
			}
		}

	case scrDiagnostics:
		switch key {
		case "esc", "q":
			m.screen = scrHome
			return m, m.refreshHomeCmd()
		case "r":
			m.doctorLoading = true
			return m, m.runDoctorCmd()
		}

	case scrSettings:
		switch key {
		case "up", "k":
			if m.cfgIdx > 0 {
				m.cfgIdx--
			}
		case "down", "j":
			if m.cfgIdx < len(m.cfgItems)-1 {
				m.cfgIdx++
			}
		case "enter", " ", "left", "right":
			if m.cfgIdx < len(m.cfgItems) {
				return m, m.cfgItems[m.cfgIdx].run(m)
			}
		case "esc", "q":
			m.screen = scrHome
			return m, m.refreshHomeCmd()
		}

	case scrAdvanced:
		if m.pinActive {
			return m.handlePinKey(msg)
		}
		switch key {
		case "up", "k":
			if m.advIdx > 0 {
				m.advIdx--
			}
		case "down", "j":
			if m.advIdx < len(m.advItems)-1 {
				m.advIdx++
			}
		case "enter":
			if m.advIdx < len(m.advItems) {
				return m, m.advItems[m.advIdx].run(m)
			}
		case "esc", "q":
			m.screen = scrHome
			return m, m.refreshHomeCmd()
		}

	case scrConfirmation:
		switch key {
		case "up", "k":
			if m.confirm.idx > 0 {
				m.confirm.idx--
			}
		case "down", "j":
			if m.confirm.idx < len(m.confirm.entries)-1 {
				m.confirm.idx++
			}
		case "enter", " ":
			if m.confirm.idx < len(m.confirm.entries) {
				return m, m.confirm.entries[m.confirm.idx].activate(m)
			}
		case "esc", "q":
			m.screen = m.confirm.back
		}

	case scrError:
		switch key {
		case "up", "k":
			if m.errSt.idx > 0 {
				m.errSt.idx--
			}
		case "down", "j":
			if m.errSt.idx < len(m.errSt.actions)-1 {
				m.errSt.idx++
			}
		case "enter":
			if m.errSt.idx < len(m.errSt.actions) {
				return m, m.errSt.actions[m.errSt.idx].run(m)
			}
		case "esc", "q":
			return m, m.errSt.actions[len(m.errSt.actions)-1].run(m)
		}
	}
	return m, nil
}

func (e confirmEntry) activate(m *Model) tea.Cmd {
	if e.toggle != nil {
		*e.toggle = !*e.toggle
		return nil
	}
	if e.run != nil {
		return e.run(m)
	}
	return nil
}

// acknowledgeNotice persists the §7 notice acknowledgement (non-blocking:
// a save failure must not trap the user).
func (m *Model) acknowledgeNotice() tea.Cmd {
	cfg := m.cfg
	cfg.NoticeAcknowledged = true
	m.cfg = cfg
	m.screen = scrHome
	if err := m.svc.SaveConfig(cfg); err != nil {
		m.homeNotice = "Note: the notice could not be saved and will be shown again next time."
	}
	return m.refreshHomeCmd()
}

// --- operations -----------------------------------------------------------

// startInstallFlow routes install-shaped requests (normal install and
// pinned installs) through systemcheck → installoptions → installing.
func (m *Model) startInstallFlow(opts app.InstallOptions) tea.Cmd {
	m.req = opRequest{kind: opInstall, install: opts}
	m.optLaunch = false
	m.optForce = false
	m.homeIdx = 0
	m.screen = scrSystemCheck
	m.doctorLoading = true
	return m.runDoctorCmd()
}

// runOp launches the pending request on the op goroutine.
func (m *Model) runOp() tea.Cmd {
	cancel, events, done := startOp(m.baseCtx, m.req, m.svc)
	m.opCancel = cancel
	m.opEvents = events
	m.opDone = done
	m.opRunning = true
	m.opKill = false
	m.resetRows()
	return waitForEvent(events, done)
}

func (m *Model) resetRows() {
	m.rows = [nStageRows]stageRow{}
}

// applyProgress folds one structured event into the progress table.
func (m *Model) applyProgress(e app.ProgressEvent) {
	idx, ok := rowFor(e)
	if !ok {
		return
	}
	// Everything strictly before the active row has completed (same
	// invariant as the CLI HUD).
	for i := 0; i < idx; i++ {
		if m.rows[i].state != rowDone {
			m.rows[i] = stageRow{state: rowDone}
		}
	}
	if e.Current != 0 || e.Total != 0 {
		m.rows[idx].annotation = percentText(e.Current, e.Total)
	}
	if m.rows[idx].state == rowPending {
		m.rows[idx].state = rowActive
	}
}

// finishOp routes the terminal outcome of an operation.
func (m *Model) finishOp(d opDoneMsg) tea.Cmd {
	m.opRunning = false
	m.opEvents, m.opDone = nil, nil

	if errors.Is(d.err, app.ErrUpToDate) {
		// §12/§30: up-to-date is an info, not a failure.
		m.screen = scrHome
		m.homeNotice = app.FriendlyMessage(d.err)
		return m.refreshHomeCmd()
	}
	if d.err != nil {
		m.goToError(d.err, d.result.LogPath, d.kind)
		return nil
	}

	m.screen = scrOpResult
	m.opRes = resultState{kind: d.kind, result: d.result, purge: d.purge}
	m.exitSummary = resultSummary(d.kind, d.result, d.purge)
	return m.refreshHomeCmd()
}

// goToError enters the §39 failure screen with the right action set.
func (m *Model) goToError(err error, logPath string, kind opKind) {
	m.screen = scrError
	m.errSt = newErrorState(err, logPath, kind, m)
	m.exitSummary = []string{"✗ " + app.FriendlyMessage(err)}
}

func newErrorState(err error, logPath string, kind opKind, m *Model) errorState {
	st := errorState{err: err, logPath: logPath}

	if errors.Is(err, app.ErrUnknownContract) {
		// §17: distinct screen and action set; the builder never guesses.
		st.actions = []errAction{
			{label: "Use installed version", run: func(m *Model) tea.Cmd {
				m.screen = scrHome
				return m.refreshHomeCmd()
			}},
			{label: "Diagnostics", run: func(m *Model) tea.Cmd {
				m.screen = scrDiagnostics
				m.doctorLoading = true
				return m.runDoctorCmd()
			}},
			{label: "Exit", run: func(m *Model) tea.Cmd { return tea.Quit }},
		}
		return st
	}

	if logPath != "" {
		st.actions = append(st.actions, errAction{label: "View log", run: func(m *Model) tea.Cmd {
			m.errSt.showDetail = !m.errSt.showDetail
			if m.errSt.detail == "" {
				m.errSt.detail = strings.Join(logTail(m.errSt.logPath, 40), "\n")
			}
			return nil
		}})
	}
	st.actions = append(st.actions, errAction{label: "Diagnostics", run: func(m *Model) tea.Cmd {
		m.screen = scrDiagnostics
		m.doctorLoading = true
		return m.runDoctorCmd()
	}})
	if kind.retryable() {
		st.actions = append(st.actions, errAction{label: "Retry", run: func(m *Model) tea.Cmd { return m.runOp() }})
	}
	st.actions = append(st.actions, errAction{label: "Back", run: func(m *Model) tea.Cmd {
		m.screen = scrHome
		return m.refreshHomeCmd()
	}})
	return st
}

// --- menus ----------------------------------------------------------------

// newConfirm builds a generic confirmation state: the caller's toggle
// entries first, then Confirm/Cancel.
func newConfirm(title, body string, entries []confirmEntry, back screen, onYes func(m *Model) tea.Cmd) confirmState {
	entries = append(entries,
		confirmEntry{label: "Confirm", run: onYes},
		confirmEntry{label: "Cancel", run: func(m *Model) tea.Cmd {
			m.screen = back
			return nil
		}},
	)
	return confirmState{title: title, body: body, entries: entries, back: back}
}

// updateConfirmBody explains the transactional update in §23 terms.
func updateConfirmBody(m *Model) string {
	current := "none"
	if m.hasInstall {
		current = m.install.AsepriteVersion
	}
	target := m.latest
	if target == "" {
		target = "the latest stable"
	}
	return "Installs the latest stable Aseprite (" + target + ") over the\ncurrent installation (" + current + ").\nThe working version is kept until the new build verifies."
}

// channelStable names the only supported channel for now; the
// development channel arrives with its backend in a later phase.
func channelStable() aseprite.Channel { return aseprite.ChannelStable }

func (m *Model) buildHomeItems() []menuItem {
	if !m.hasInstall {
		return []menuItem{
			{label: "Install Aseprite", run: func(m *Model) tea.Cmd {
				return m.startInstallFlow(app.InstallOptions{Channel: channelStable()})
			}},
			{label: "Advanced", run: func(m *Model) tea.Cmd { return m.enterAdvanced() }},
			{label: "Settings", run: func(m *Model) tea.Cmd { return m.enterSettings() }},
			{label: "Diagnostics (doctor)", run: func(m *Model) tea.Cmd { return m.enterDiagnostics() }},
			{label: "Exit", run: func(m *Model) tea.Cmd { return tea.Quit }},
		}
	}
	return []menuItem{
		{label: "Launch Aseprite", run: func(m *Model) tea.Cmd { return m.launchCmd() }},
		{label: "Update Aseprite", run: func(m *Model) tea.Cmd {
			m.req = opRequest{kind: opUpdate, install: app.InstallOptions{}}
			m.screen = scrConfirmation
			m.confirm = newConfirm("Update Aseprite?", updateConfirmBody(m), nil, scrHome,
				func(m *Model) tea.Cmd { return m.runOp() })
			return nil
		}},
		{label: "Repair", run: func(m *Model) tea.Cmd {
			m.req = opRequest{kind: opRepair}
			m.screen = scrConfirmation
			m.confirm = newConfirm("Repair Aseprite?",
				"Validates the installed Aseprite bundle and rebuilds it\nwhen it is damaged. A healthy installation is left as is.", nil, scrHome,
				func(m *Model) tea.Cmd { return m.runOp() })
			return nil
		}},
		{label: "Advanced", run: func(m *Model) tea.Cmd { return m.enterAdvanced() }},
		{label: "Settings", run: func(m *Model) tea.Cmd { return m.enterSettings() }},
		{label: "Diagnostics (doctor)", run: func(m *Model) tea.Cmd { return m.enterDiagnostics() }},
		{label: "Exit", run: func(m *Model) tea.Cmd { return tea.Quit }},
	}
}

func (m *Model) enterAdvanced() tea.Cmd {
	m.screen = scrAdvanced
	m.advIdx = 0
	m.advItems = []menuItem{
		{label: "Install a specific version…", run: func(m *Model) tea.Cmd {
			m.pinActive = true
			m.pinned = ""
			return nil
		}},
		{label: "Rebuild current version", run: func(m *Model) tea.Cmd {
			m.req = opRequest{kind: opRebuild}
			m.screen = scrConfirmation
			m.confirm = newConfirm("Rebuild Aseprite?",
				"Recompiles the installed version from its source.\nYour user data is preserved; the working\ninstallation is only replaced after verification.", nil, scrAdvanced,
				func(m *Model) tea.Cmd { return m.runOp() })
			return nil
		}},
		{label: "Clean rebuild", run: func(m *Model) tea.Cmd {
			m.req = opRequest{kind: opRebuild, rebuild: app.RebuildOptions{Clean: true}}
			m.screen = scrConfirmation
			m.confirm = newConfirm("Clean rebuild?",
				"Discards the current build output and the extracted\nsource, then rebuilds from a fresh official extraction.\nYour user data is preserved.", nil, scrAdvanced,
				func(m *Model) tea.Cmd { return m.runOp() })
			return nil
		}},
		{label: "Uninstall Aseprite…", run: func(m *Model) tea.Cmd { return m.enterUninstallConfirm() }},
		{label: "Back", run: func(m *Model) tea.Cmd {
			m.screen = scrHome
			return m.refreshHomeCmd()
		}},
	}
	return nil
}

func (m *Model) enterSettings() tea.Cmd {
	m.screen = scrSettings
	m.cfgIdx = 0
	m.cfgLoading = true
	m.cfgItems = nil
	return m.loadConfigCmd()
}

// buildSettingsItems materializes the toggle list from the loaded
// config. Labels are rendered from m.cfg at view time, so entries stay
// in sync after each flip. The channel is not a toggle: stable is the
// only channel until the development backend ships — no dead items.
func (m *Model) buildSettingsItems() []menuItem {
	return []menuItem{
		{label: "Keep build workspace", run: func(m *Model) tea.Cmd {
			m.cfg.KeepWorkspace = !m.cfg.KeepWorkspace
			return m.saveConfigCmd(m.cfg)
		}},
		{label: "Verbose operation logs", run: func(m *Model) tea.Cmd {
			m.cfg.Verbose = !m.cfg.Verbose
			return m.saveConfigCmd(m.cfg)
		}},
		{label: "Back", run: func(m *Model) tea.Cmd {
			m.screen = scrHome
			return m.refreshHomeCmd()
		}},
	}
}

func (m *Model) saveConfigCmd(cfg state.Config) tea.Cmd {
	return func() tea.Msg {
		err := m.svc.SaveConfig(cfg)
		return savedMsg{err: err}
	}
}

type savedMsg struct{ err error }

func (m *Model) enterDiagnostics() tea.Cmd {
	m.screen = scrDiagnostics
	m.doctorLoading = true
	return m.runDoctorCmd()
}

func (m *Model) enterUninstallConfirm() tea.Cmd {
	m.screen = scrConfirmation
	// Every scope starts off — the user opts in explicitly. The
	// previous-version entry only appears when one was retained.
	var removePrev bool
	purgeSources, purgeTools, purgeSkia, purgeLogs := false, false, false, false
	var entries []confirmEntry
	if m.hasInstall && m.install.PreviousAppPath != "" {
		entries = append(entries, confirmEntry{label: "Also remove the retained previous version", toggle: &removePrev})
	}
	entries = append(entries,
		confirmEntry{label: "Also remove downloaded Aseprite sources", toggle: &purgeSources},
		confirmEntry{label: "Also remove CMake/Ninja caches", toggle: &purgeTools},
		confirmEntry{label: "Also remove Skia cache", toggle: &purgeSkia},
		confirmEntry{label: "Also remove operation logs", toggle: &purgeLogs},
		confirmEntry{label: "Confirm removal", run: func(m *Model) tea.Cmd {
			m.req = opRequest{
				kind:      opUninstall,
				uninstall: app.UninstallOptions{RemovePrevious: removePrev},
				purge:     app.PurgeOptions{Sources: purgeSources, Tools: purgeTools, Skia: purgeSkia, Logs: purgeLogs},
			}
			return m.runOp()
		}},
		confirmEntry{label: "Cancel", run: func(m *Model) tea.Cmd {
			m.screen = scrAdvanced
			return nil
		}},
	)
	m.confirm = confirmState{
		title:   "Uninstall Aseprite?",
		body:    "Only files installed by aseprite-builder are removed.\nYour artwork and projects are never touched.",
		entries: entries,
		back:    scrAdvanced,
	}
	return nil
}

// handlePinKey drives the pinned-version text entry on the advanced
// screen.
func (m *Model) handlePinKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.pinned == "" {
			return m, nil
		}
		m.pinActive = false
		return m, m.startInstallFlow(app.InstallOptions{
			Version: m.pinned,
			Channel: channelStable(),
		})
	case "esc":
		m.pinActive = false
		return m, nil
	case "backspace":
		if m.pinned != "" {
			r := []rune(m.pinned)
			m.pinned = string(r[:len(r)-1])
		}
		return m, nil
	default:
		if msg.Type == tea.KeyRunes {
			m.pinned += string(msg.Runes)
		}
		return m, nil
	}
}

// installOptionItems builds the installoptions entries; the screen
// navigation index reuses homeIdx.
func (m *Model) installOptionItems() []menuItem {
	launch, force := m.optLaunch, m.optForce
	return []menuItem{
		{label: toggleLabel("Launch Aseprite after the install", launch), run: func(m *Model) tea.Cmd {
			m.optLaunch = !m.optLaunch
			return nil
		}},
		{label: toggleLabel("Replace an Aseprite.app not installed by this tool", force), run: func(m *Model) tea.Cmd {
			m.optForce = !m.optForce
			return nil
		}},
		{label: "Start installation", run: func(m *Model) tea.Cmd {
			opts := m.req.install
			opts.LaunchAfterInstall = m.optLaunch
			opts.ForceOverwriteUnowned = m.optForce
			m.req.install = opts
			m.screen = scrInstalling
			return m.runOp()
		}},
		{label: "Cancel", run: func(m *Model) tea.Cmd {
			m.screen = scrHome
			return m.refreshHomeCmd()
		}},
	}
}
