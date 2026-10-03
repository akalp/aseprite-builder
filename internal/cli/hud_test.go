package cli

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/aseprite-builder/aseprite-builder/internal/app"
)

func TestHUDSequenceMatchesMock(t *testing.T) {
	var out bytes.Buffer
	hud := NewHUD(&out)
	hud.ForceTTY = true

	// Simulate the exact pipeline event flow.
	hud.Event(app.ProgressEvent{Stage: app.StageDetect})
	hud.Event(app.ProgressEvent{Stage: app.StageFetchSource})
	hud.Event(app.ProgressEvent{Stage: app.StageResolveTools})
	hud.Event(app.ProgressEvent{Stage: app.StageResolveSkia})
	hud.Event(app.ProgressEvent{Stage: app.StageBuild, Message: app.BuildPhaseConfigure})
	hud.Event(app.ProgressEvent{Stage: app.StageBuild, Message: app.BuildPhaseCompile, Current: 922, Total: 1470})
	hud.Event(app.ProgressEvent{Stage: app.StageVerify})
	hud.Event(app.ProgressEvent{Stage: app.StageInstall})
	hud.Close(true)

	text := out.String()
	// The final render must match the user-facing mock table.
	for _, line := range []string{
		"Checking system            ✓",
		"Downloading source         ✓",
		"Resolving tools            ✓",
		"Preparing Skia             ✓",
		"Configuring build          ✓",
		"Compiling Aseprite         ✓",
		"Verifying                  ✓",
		"Installing application     ✓",
	} {
		if !containsLine(text, line) {
			t.Errorf("missing line %q in:\n%s", line, text)
		}
	}
}

func TestHUDPercentageShown(t *testing.T) {
	var out bytes.Buffer
	hud := NewHUD(&out)
	hud.ForceTTY = true

	hud.Event(app.ProgressEvent{Stage: app.StageBuild, Message: app.BuildPhaseConfigure})
	hud.Event(app.ProgressEvent{Stage: app.StageBuild, Message: app.BuildPhaseCompile, Current: 922, Total: 1470})
	// 922/1470 ≈ 62.7% → "63%"
	if !containsLine(out.String(), "Compiling Aseprite         63%") {
		t.Errorf("output =\n%s", out.String())
	}
	// Configure row completed once compilation starts.
	if !containsLine(out.String(), "Configuring build          ✓") {
		t.Errorf("output =\n%s", out.String())
	}
}

func TestHUDPendingRowsBeforeStages(t *testing.T) {
	var out bytes.Buffer
	hud := NewHUD(&out)
	hud.ForceTTY = true

	hud.Event(app.ProgressEvent{Stage: app.StageDetect})
	text := out.String()
	if !containsLine(text, "Downloading source         ○") {
		t.Errorf("pending rows must render as ○; got:\n%s", text)
	}
	if !containsLine(text, "Checking system            …") {
		t.Errorf("active row must render as …; got:\n%s", text)
	}
}

func TestHUDCloseFailsKeepsState(t *testing.T) {
	var out bytes.Buffer
	hud := NewHUD(&out)
	hud.ForceTTY = true

	hud.Event(app.ProgressEvent{Stage: app.StageDetect})
	hud.Event(app.ProgressEvent{Stage: app.StageFetchSource})
	hud.Event(app.ProgressEvent{Stage: app.StageBuild, Message: app.BuildPhaseConfigure})
	hud.Close(false)

	text := out.String()
	if !containsLine(text, "Configuring build          …") {
		t.Errorf("failed run should keep the active row; got:\n%s", text)
	}
	if !containsLine(text, "Installing application     ○") {
		t.Errorf("pending rows should remain pending; got:\n%s", text)
	}
}

func TestPercentText(t *testing.T) {
	tests := []struct {
		done, total int64
		want        string
	}{
		{0, 0, ""},    // unknown total
		{0, 10, "0%"}, // total known, nothing done
		{5, 10, "50%"},
		{922, 1470, "63%"},
		{2000, 1470, "100%"},
		{-1, 10, "0%"},
	}
	for _, tc := range tests {
		if got := percentText(tc.done, tc.total); got != tc.want {
			t.Errorf("percentText(%d,%d) = %q, want %q", tc.done, tc.total, got, tc.want)
		}
	}
}

func TestHUDRowMapping(t *testing.T) {
	h := NewHUD(&bytes.Buffer{})
	tests := map[app.ProgressEvent]int{
		{Stage: app.StageDetect}:                                  0,
		{Stage: app.StageSystemRequirements}:                      0,
		{Stage: app.StageResolveRelease}:                          0,
		{Stage: app.StageInspectContract}:                         0,
		{Stage: app.StageFetchSource}:                             1,
		{Stage: app.StageResolveTools}:                            2,
		{Stage: app.StageResolveSkia}:                             3,
		{Stage: app.StageBuild, Message: app.BuildPhaseConfigure}: 4,
		{Stage: app.StageBuild, Message: app.BuildPhaseCompile}:   5,
		{Stage: app.StageVerify}:                                  6,
		{Stage: app.StageInstall}:                                 7,
		{Stage: app.StageCleanup}:                                 0, // cleanup maps nowhere (ok=false)
	}
	for e, want := range tests {
		got, ok := h.rowFor(e)
		if e.Stage == app.StageCleanup {
			if ok {
				t.Errorf("cleanup should be unmappable, got row %d", got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("rowFor(%+v) = %d,%v want %d", e, got, ok, want)
		}
	}
}

// containsLine checks whether the rendered block contains the exact
// line (BD characters from the clear-escape are not included since the
// test compares visible content).
func containsLine(text, line string) bool {
	for _, l := range splitLines(text) {
		if trimANSI(l) == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// trimANSI strips CSI escape sequences used by the HUD (cursor moves
// and line clears) before comparing visible content.
var ansiCSIRe = regexp.MustCompile(`\x1b\[[0-9]*[A-Za-z]`)

func trimANSI(s string) string {
	return ansiCSIRe.ReplaceAllString(s, "")
}
