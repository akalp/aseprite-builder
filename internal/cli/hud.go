package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/aseprite-builder/aseprite-builder/internal/app"
)

// HUD rows displayed during install/update/rebuild, in this order:
//
//	Checking system            ✓
//	Downloading source         ✓
//	Resolving tools            ✓
//	Preparing Skia             ✓
//	Configuring build          ✓
//	Compiling Aseprite         63%
//	Verifying                  ✓
//	Installing application     ○
type hudRow struct{ label string }

var hudRows = []hudRow{
	{label: "Checking system"},
	{label: "Downloading source"},
	{label: "Resolving tools"},
	{label: "Preparing Skia"},
	{label: "Configuring build"},
	{label: "Compiling Aseprite"},
	{label: "Verifying"},
	{label: "Installing application"},
}

type hudRowState int

const (
	rowPending hudRowState = iota
	rowActive
	rowDone
)

// HUD renders the pipeline as a fixed table on a terminal, updating in
// place when out is a TTY, and printing transition lines otherwise.
type HUD struct {
	// Out receives the render (normally the CLI's stderr).
	Out io.Writer
	// ForceTTY skips tty detection (tests).
	ForceTTY bool

	states       []hudRowState
	annotations  map[int]string // percent-ish right column for the active row
	doneSeen     []bool         // non-TTY dedup bookkeeping
	lastActive   int            // non-TTY dedup bookkeeping
	renderedRows int            // TTY bookkeeping
}

// NewHUD returns a HUD writing to w.
func NewHUD(w io.Writer) *HUD {
	return &HUD{
		Out:         w,
		states:      make([]hudRowState, len(hudRows)),
		annotations: map[int]string{},
		doneSeen:    make([]bool, len(hudRows)),
		lastActive:  -1,
	}
}

// Sink adapts the HUD into an app.ProgressSink.
func (h *HUD) Sink() app.ProgressSink { return h.Event }

// Event consumes one app progress event.
func (h *HUD) Event(e app.ProgressEvent) {
	idx, ok := h.rowFor(e)
	if !ok {
		return
	}
	// Everything strictly before the active row has completed.
	for i := 0; i < idx; i++ {
		if h.states[i] != rowPending {
			h.states[i] = rowDone
			delete(h.annotations, i)
		}
	}
	if e.Current != 0 || e.Total != 0 {
		h.annotations[idx] = percentText(e.Current, e.Total)
	}
	if h.states[idx] == rowPending {
		h.states[idx] = rowActive
	}
	h.render()
}

// rowFor maps an app stage (or build phase tick) to a row index.
func (h *HUD) rowFor(e app.ProgressEvent) (int, bool) {
	if e.Stage == app.StageBuild {
		// Build ticks carry the phase name; anything else on the build
		// stage (defensive) maps to the compile row.
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

// Close finalizes the HUD. On success every touched row becomes ✓; the
// table then stays as a summary.
func (h *HUD) Close(success bool) {
	if success {
		for i := range h.states {
			if h.states[i] != rowPending {
				h.states[i] = rowDone
				delete(h.annotations, i)
			}
		}
	}
	h.render()
}

// isTTY reports whether out is an interactive terminal.
func (h *HUD) isTTY() bool {
	if h.ForceTTY {
		return true
	}
	if f, ok := h.Out.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		return fi.Mode()&os.ModeCharDevice != 0
	}
	return false
}

// render draws the current state: in place on TTYs, otherwise one line
// per unseen transition.
func (h *HUD) render() {
	if h.isTTY() {
		h.renderInPlace()
		return
	}
	for i, st := range h.states {
		switch {
		case st == rowDone && !h.doneSeen[i]:
			fmt.Fprintf(h.Out, "✓ %s\n", hudRows[i].label)
			h.doneSeen[i] = true
		case st == rowActive && h.lastActive != i:
			fmt.Fprintf(h.Out, "▸ %s\n", hudRows[i].label)
			h.lastActive = i
		}
	}
}

// renderInPlace redraws the fixed block, keeping the cursor stable.
func (h *HUD) renderInPlace() {
	if h.renderedRows > 0 {
		// Move the cursor back up to the top of the block.
		fmt.Fprintf(h.Out, "\x1b[%dA", h.renderedRows)
	}
	width := h.labelWidth()
	for i, row := range hudRows {
		fmt.Fprintf(h.Out, "\x1b[2K%s %s\n", padTo(row.label, width), h.rightText(i))
	}
	h.renderedRows = len(hudRows)
}

func (h *HUD) labelWidth() int {
	const minWidth = 26 // column width of the reference mock
	w := minWidth
	for _, row := range hudRows {
		if n := utf8.RuneCountInString(row.label); n > w {
			w = n
		}
	}
	return w
}

// rightText is the two-space-gapped right column per the product mock.
func (h *HUD) rightText(i int) string {
	switch h.states[i] {
	case rowDone:
		return "✓"
	case rowActive:
		if ann := h.annotations[i]; ann != "" {
			return ann
		}
		return "…"
	default:
		return "○"
	}
}

// padTo right-pads s with spaces to n display columns (labels are
// plain, so rune count is the display width).
func padTo(s string, n int) string {
	gap := n - utf8.RuneCountInString(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// percentText formats done/total as a percentage string ("" when the
// total is unknown; rounded and clamped to [0,100]).
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
