package tui

// Rendering for every screen (spec §12 home mock, §34 state list,
// §39 failure UX). All facts shown here originate in the app service
// layer; this file is presentation only.

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/akalp/aseprite-builder/internal/app"
)

// --- styles ----------------------------------------------------------------

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	subStyle     = lipgloss.NewStyle().Faint(true)
	headingStyle = lipgloss.NewStyle().Bold(true)
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	failStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	detailStyle  = lipgloss.NewStyle().Faint(true)
	activeStyle  = lipgloss.NewStyle().Bold(true)
	cursorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4")) // blue
	inputStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	footerStyle  = lipgloss.NewStyle().Faint(true)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2)

	// Visually distinct treatment for unknown-contract failures (§17):
	// amber frame instead of the default one.
	contractPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(lipgloss.Color("3")).
				Padding(1, 2)
)

func lipglossJoin(lines []string) string {
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// View renders the current screen.
func (m *Model) View() string {
	var body string
	knownContract := false
	switch m.screen {
	case scrWelcome:
		body = m.viewWelcome()
	case scrHome:
		body = m.viewHome()
	case scrSystemCheck:
		body = m.viewSystemCheck()
	case scrInstallOptions:
		body = m.viewInstallOptions()
	case scrInstalling:
		body = m.viewInstalling()
	case scrOpResult:
		body = m.viewOpResult()
	case scrDiagnostics:
		body = m.viewDiagnostics()
	case scrSettings:
		body = m.viewSettings()
	case scrAdvanced:
		body = m.viewAdvanced()
	case scrConfirmation:
		body = m.viewConfirmation()
	case scrError:
		body, knownContract = m.viewError()
	default:
		body = ""
	}

	panel := panelStyle
	if knownContract {
		panel = contractPanelStyle
	}

	title := titleStyle.Render("Aseprite Source Builder")
	subtitle := subStyle.Render("Unofficial community tool")
	return panel.Render(lipglossJoin([]string{title, subtitle, "", body})) +
		"\n" + footerStyle.Render(m.footerText())
}

func (m *Model) footerText() string {
	if m.opRunning {
		return "ctrl+c cancel operation (twice quits) · aseprite-builder " + m.builderVersion
	}
	return "↑/↓ move · enter select · esc back · q quit · aseprite-builder " + m.builderVersion
}

// menu renders a selectable list with the given index.
func menu(items []string, idx int) string {
	var b strings.Builder
	for i, label := range items {
		mark := "  "
		if i == idx {
			mark = cursorStyle.Render("> ")
		}
		b.WriteString(mark + label + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func toggleLabel(label string, on bool) string {
	if on {
		return "[x] " + label
	}
	return "[ ] " + label
}

// checkRow renders one doctor row with a ✓/✗ mark.
func checkRow(r app.DoctorRow, nameWidth int) string {
	mark := okStyle.Render("✓")
	if !r.OK {
		mark = failStyle.Render("✗")
	}
	return fmt.Sprintf("%s %-*s  %s", mark, nameWidth, r.Name, r.Detail)
}

func doctorNameWidth(rows []app.DoctorRow) int {
	w := len("Installation")
	for _, r := range rows {
		if l := utf8.RuneCountInString(r.Name); l > w {
			w = l
		}
	}
	return w
}

// --- welcome ---------------------------------------------------------------

func (m *Model) viewWelcome() string {
	return lipglossJoin([]string{
		headingStyle.Render("Before your first installation"),
		"",
		"This builder is unofficial and is not affiliated with or",
		"endorsed by Aseprite or Igara Studio.",
		"",
		"Aseprite will be downloaded from its official source and",
		"compiled on this machine. Nothing is redistributed by",
		"aseprite-builder.",
		"",
		"Your use of Aseprite remains subject to its license:",
		"https://www.aseprite.org/faq/#can-i-compile-aseprite-myself",
		"",
		"Press enter to continue.",
	})
}

// --- home ------------------------------------------------------------------

func (m *Model) viewHome() string {
	var lines []string

	switch {
	case m.homeErr != nil:
		lines = append(lines, warnStyle.Render("Could not read the installation state: "+m.homeErr.Error()))
	case !m.homeLoaded:
		lines = append(lines, "Reading installation state…")
	case m.hasInstall:
		lines = append(lines,
			fmt.Sprintf("Aseprite %s installed.", m.install.AsepriteVersion),
			detailStyle.Render(m.install.AppPath))
	default:
		lines = append(lines, "Aseprite is not installed.")
	}
	lines = append(lines, "", m.viewLatestLine())

	if m.homeNotice != "" {
		lines = append(lines, "", okStyle.Render(m.homeNotice))
	}

	labels := make([]string, 0, len(m.homeItems))
	for _, it := range m.homeItems {
		labels = append(labels, it.label)
	}
	lines = append(lines, "", menu(labels, m.homeIdx))
	return lipglossJoin(lines)
}

func (m *Model) viewLatestLine() string {
	switch {
	case m.latestLoading:
		return detailStyle.Render("Latest stable: checking…")
	case m.latestErr != nil:
		return detailStyle.Render("Latest stable: unknown (check your network)")
	}
	line := "Latest stable: " + m.latest
	if m.hasInstall && m.latest != "" {
		if m.latest == m.install.AsepriteVersion {
			line += "  " + okStyle.Render("up to date")
		} else {
			line += "  " + warnStyle.Render("update available")
		}
	}
	return line
}

// --- systemcheck -----------------------------------------------------------

func (m *Model) viewSystemCheck() string {
	if m.doctorLoading {
		return "Checking your system…"
	}
	if m.doctorErr != nil {
		return app.FriendlyMessage(m.doctorErr)
	}

	rows := m.doctor.DoctorRows()
	w := doctorNameWidth(rows)
	lines := []string{headingStyle.Render("Checking your system…"), ""}
	for _, r := range rows {
		lines = append(lines, checkRow(r, w))
	}
	lines = append(lines, "",
		"Continue installation? enter to continue · esc to cancel")
	return lipglossJoin(lines)
}

// --- installoptions --------------------------------------------------------

func (m *Model) viewInstallOptions() string {
	target := "the latest stable release"
	if v := m.req.install.Version; v != "" {
		target = "Aseprite " + v + " (pinned)"
	} else if m.latest != "" {
		target = "Aseprite " + m.latest + " (latest stable)"
	}

	var labels []string
	for _, it := range m.installOptionItems() {
		labels = append(labels, it.label)
	}

	return lipglossJoin([]string{
		headingStyle.Render("Ready to install"),
		"",
		"Will install: " + target,
		"",
		menu(labels, m.homeIdx),
		"",
		"enter selects · esc cancels",
	})
}

// --- installing ------------------------------------------------------------

func (m *Model) viewInstalling() string {
	lines := []string{headingStyle.Render(opLabel(m.req.kind)), ""}
	for i, row := range m.rows {
		switch row.state {
		case rowDone:
			lines = append(lines, okStyle.Render("✓")+" "+stageRowLabels[i])
		case rowActive:
			annotation := row.annotation
			if annotation == "" {
				annotation = "…"
			}
			lines = append(lines, activeStyle.Render("▸")+" "+activeStyle.Render(stageRowLabels[i])+
				strings.Repeat(" ", maxInt(2, 24-utf8.RuneCountInString(stageRowLabels[i])))+
				detailStyle.Render(annotation))
		default:
			lines = append(lines, "○ "+stageRowLabels[i])
		}
	}
	return lipglossJoin(lines)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// opLabel phrases the running operation.
func opLabel(kind opKind) string {
	switch kind {
	case opInstall:
		return "Installing Aseprite"
	case opUpdate:
		return "Updating Aseprite"
	case opRebuild:
		return "Rebuilding Aseprite"
	case opRepair:
		return "Repairing Aseprite"
	case opUninstall:
		return "Removing Aseprite"
	default:
		return "Working"
	}
}

// --- op result -------------------------------------------------------------

func (m *Model) viewOpResult() string {
	lines := []string{headingStyle.Render("Done"), ""}
	lines = append(lines, resultSummary(m.opRes.kind, m.opRes.result, m.opRes.purge)...)
	if m.opRes.kind == opInstall && m.opRes.result.AppPath != "" {
		lines = append(lines, "", "l: launch Aseprite now")
	}
	lines = append(lines, "", "enter: back to the main menu")
	return lipglossJoin(lines)
}

// resultSummary builds the durable ✅ lines shown on the result screen
// and printed to stderr after the program exits — the same facts the
// CLI prints at the end of each command.
func resultSummary(kind opKind, r app.InstallResult, purge []app.PurgeResult) []string {
	var out []string
	switch kind {
	case opInstall:
		out = append(out, fmt.Sprintf("✅ Aseprite %s installed: %s", r.Version, r.AppPath))
		out = append(out, "   Launch it with: aseprite-builder launch")
	case opUpdate:
		out = append(out, fmt.Sprintf("✅ Updated to Aseprite %s: %s", r.Version, r.AppPath))
	case opRebuild:
		out = append(out, fmt.Sprintf("✅ Rebuilt Aseprite %s: %s", r.Version, r.AppPath))
	case opRepair:
		out = append(out, fmt.Sprintf("✅ Aseprite %s is installed and healthy at %s", r.Version, r.AppPath))
	case opUninstall:
		out = append(out, "✅ Removed the application.")
	}
	for _, p := range purge {
		if p.Err != nil {
			out = append(out, fmt.Sprintf("   note: could not remove %s (%s)", p.What, p.Dir))
		} else {
			out = append(out, fmt.Sprintf("   removed %s", p.Dir))
		}
	}
	return out
}

// --- diagnostics -----------------------------------------------------------

func (m *Model) viewDiagnostics() string {
	if m.doctorLoading {
		return "Running diagnostics…"
	}
	if m.doctorErr != nil {
		return app.FriendlyMessage(m.doctorErr)
	}

	rows := m.doctor.DoctorRows()
	w := doctorNameWidth(rows)
	lines := []string{headingStyle.Render("Diagnostics"), ""}
	for _, r := range rows {
		lines = append(lines, checkRow(r, w))
	}
	lines = append(lines, "")
	if m.doctor.Status == "healthy" {
		lines = append(lines, okStyle.Render("Everything looks good."))
	} else {
		lines = append(lines, warnStyle.Render("Some checks failed. See above."))
	}
	lines = append(lines, "", "r: re-run · esc: back")
	return lipglossJoin(lines)
}

// --- settings --------------------------------------------------------------

func (m *Model) viewSettings() string {
	if m.cfgLoading {
		return "Reading settings…"
	}
	labels := make([]string, 0, len(m.cfgItems))
	for _, it := range m.cfgItems {
		switch it.label {
		case "Keep build workspace":
			labels = append(labels, toggleLabel(it.label, m.cfg.KeepWorkspace))
		case "Verbose operation logs":
			labels = append(labels, toggleLabel(it.label, m.cfg.Verbose))
		default:
			labels = append(labels, it.label)
		}
	}
	return lipglossJoin([]string{
		headingStyle.Render("Settings"),
		"",
		"Channel: stable",
		detailStyle.Render("  (the development channel arrives with a later phase)"),
		"",
		menu(labels, m.cfgIdx),
	})
}

// --- advanced --------------------------------------------------------------

func (m *Model) viewAdvanced() string {
	labels := make([]string, 0, len(m.advItems))
	for _, it := range m.advItems {
		labels = append(labels, it.label)
	}
	lines := []string{
		headingStyle.Render("Advanced"),
		"",
		menu(labels, m.advIdx),
	}
	if m.pinActive {
		lines = append(lines, "",
			"Version to install (e.g. 1.3.18.6):",
			inputStyle.Render("> "+m.pinned+"_"),
			"enter: continue · esc: back")
	}
	return lipglossJoin(lines)
}

// --- confirmation ----------------------------------------------------------

func (m *Model) viewConfirmation() string {
	labels := make([]string, 0, len(m.confirm.entries))
	for _, e := range m.confirm.entries {
		labels = append(labels, e.label)
	}
	return lipglossJoin([]string{
		headingStyle.Render(m.confirm.title),
		"",
		m.confirm.body,
		"",
		menu(labels, m.confirm.idx),
	})
}

// --- error -----------------------------------------------------------------

func (m *Model) viewError() (body string, knownContract bool) {
	knownContract = errors.Is(m.errSt.err, app.ErrUnknownContract)

	lines := []string{failStyle.Render("✗ " + app.FriendlyMessage(m.errSt.err))}
	if m.errSt.showDetail && m.errSt.detail != "" {
		lines = append(lines, "", detailStyle.Render(m.errSt.detail))
	}
	if m.errSt.logPath != "" && !m.errSt.showDetail {
		lines = append(lines, "", detailStyle.Render("Full details: "+m.errSt.logPath))
	}

	labels := make([]string, 0, len(m.errSt.actions))
	for _, a := range m.errSt.actions {
		labels = append(labels, a.label)
	}
	lines = append(lines, "", menu(labels, m.errSt.idx))
	return lipglossJoin(lines), knownContract
}
