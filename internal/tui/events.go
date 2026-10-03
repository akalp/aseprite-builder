package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/akalp/aseprite-builder/internal/app"
)

// The TUI is a thin translator: user choices become Service calls, and
// app.ProgressEvent streams become view state (spec §34). This file
// holds the message plumbing; no installer logic lives here or in the
// model.

// progressMsg carries one structured pipeline event to the model.
type progressMsg struct{ e app.ProgressEvent }

// opDoneMsg carries the terminal outcome of one operation. kind is
// echoed back so result screens can phrase success per operation.
type opDoneMsg struct {
	kind   opKind
	result app.InstallResult
	purge  []app.PurgeResult
	err    error
}

// opRequest describes one runnable operation, created from user
// choices and passed to the service layer.
type opRequest struct {
	kind      opKind
	install   app.InstallOptions
	rebuild   app.RebuildOptions
	uninstall app.UninstallOptions
	purge     app.PurgeOptions
}

// startOp runs one service operation on a goroutine, streaming
// ProgressEvents into an event channel and delivering the final
// outcome on a separate, guaranteed channel (events may be dropped
// when the view is slow; the result never is).
//
// The returned cancel function stops the operation: every service
// honors its context (downloads and the process runner are
// cancellable). The TUI calls it for Ctrl+C and again on program exit
// so a quitting TUI never leaves a pipeline running in the
// background.
func startOp(parent context.Context, req opRequest, svc Service) (context.CancelFunc, <-chan tea.Msg, <-chan tea.Msg) {
	ctx, cancel := context.WithCancel(parent)
	events := make(chan tea.Msg, 16)
	done := make(chan tea.Msg, 1)

	go func() {
		sink := app.ProgressSink(func(e app.ProgressEvent) {
			select {
			case events <- progressMsg{e}:
			default: // view is busy; a dropped progress tick is harmless
			}
		})
		var res app.InstallResult
		var err error
		switch req.kind {
		case opInstall:
			res, err = svc.Install(ctx, req.install, sink)
		case opUpdate:
			res, err = svc.Update(ctx, req.install, sink)
		case opRebuild:
			res, err = svc.Rebuild(ctx, req.rebuild, sink)
		case opRepair:
			res, err = svc.Repair(ctx, sink)
		case opUninstall:
			if err = svc.Uninstall(ctx, req.uninstall); err == nil {
				done <- opDoneMsg{kind: req.kind, purge: svc.PurgeCaches(req.purge)}
				return
			}
		default:
			err = context.Canceled
		}
		done <- opDoneMsg{kind: req.kind, result: res, err: err}
	}()
	return cancel, events, done
}

// waitForEvent delivers the next message from a running operation.
// The model re-issues this command after every progress event; it
// naturally terminates once the done message arrives (the model stops
// re-issuing, and Update drains any remaining buffered events then).
// All event sends happen before the done send in the same goroutine,
// so draining events first is race-free.
func waitForEvent(events <-chan tea.Msg, done <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		select {
		case m := <-events:
			return m
		default:
		}
		select {
		case m := <-events:
			return m
		case d := <-done:
			return d
		}
	}
}

// Stage rows mirror the CLI HUD's fixed table (internal/cli/hud.go):
// same labels, same order, same stage mapping — the facts come from
// app.ProgressEvent in both frontends.
const nStageRows = 8

var stageRowLabels = [nStageRows]string{
	"Checking system",
	"Downloading source",
	"Resolving tools",
	"Preparing Skia",
	"Configuring build",
	"Compiling Aseprite",
	"Verifying",
	"Installing application",
}

// rowFor maps a progress event to a HUD row index (row, true) or
// ignores it (0, false) — identical mapping rules to the CLI HUD.
func rowFor(e app.ProgressEvent) (int, bool) {
	if e.Stage == app.StageBuild {
		if e.Message == app.BuildPhaseConfigure {
			return 4, true
		}
		return 5, true
	}
	switch e.Stage {
	case app.StageDetect, app.StageSystemRequirements, app.StageResolveRelease, app.StageInspectContract:
		return 0, true
	case app.StageFetchSource:
		return 1, true
	case app.StageResolveTools:
		return 2, true
	case app.StageResolveSkia:
		return 3, true
	case app.StageVerify:
		return 6, true
	case app.StageInstall:
		return 7, true
	default:
		return 0, false
	}
}

// logTail returns the last n lines of a log file, "" when unreadable.
// Pure presentation: the file is written by the service layer.
func logTail(path string, n int) []string {
	data, err := os.ReadFile(path) //nolint:gosec // path produced by the app layer
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// percentText formats done/total as a percentage string ("" when the
// total is unknown; rounded and clamped to [0,100]) — identical to the
// CLI HUD's formatting so both frontends show the same number.
func percentText(done, total int64) string {
	if total <= 0 {
		return ""
	}
	pct := int64(float64(done)/float64(total)*100 + 0.5)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%d%%", pct)
}
