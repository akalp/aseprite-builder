package tui

// Bootstrap of the interactive program (spec §30: bare invocation
// opens the TUI). main.go hands in the shared service wiring and the
// builder version; everything else is Bubble Tea.

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the interactive TUI and returns the process exit code.
//
// The operation context is derived from the parent (signal-wired by
// main). When the program exits while an operation is still running,
// the operation context is canceled here so no pipeline is ever left
// running behind a dead TUI.
func Run(parent context.Context, svc Service, builderVersion string) int {
	m := NewModel(parent, svc, builderVersion)
	p := tea.NewProgram(m, tea.WithAltScreen())

	out, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "aseprite-builder: interactive interface failed: %v\n", err)
		return 1
	}
	if final, ok := out.(*Model); ok {
		if final.opCancel != nil && final.opRunning {
			final.opCancel()
		}
		for _, line := range final.exitSummary {
			fmt.Fprintln(os.Stderr, line)
		}
	}
	return 0
}

// isTerminal reports whether f is an interactive terminal; the TUI is
// only opened for real terminals (piped stdout keeps the scriptable
// usage output).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ShouldOpen reports whether a bare invocation should open the TUI:
// no arguments and an interactive terminal on both stdin and stdout.
func ShouldOpen(args []string, stdin, stdout *os.File) bool {
	return len(args) == 0 && isTerminal(stdin) && isTerminal(stdout)
}
