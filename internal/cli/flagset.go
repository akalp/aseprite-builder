package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// commandSummaries maps command names to their one-line summaries,
// shared by per-command help and the top-level usage listing.
var commandSummaries = map[string]string{
	"install":   "Build and install the latest stable Aseprite",
	"update":    "Update to the latest stable Aseprite (transactional)",
	"rebuild":   "Rebuild the installed Aseprite version",
	"launch":    "Launch the installed Aseprite",
	"repair":    "Check the installation and rebuild if broken",
	"uninstall": "Remove the builder-installed Aseprite",
	"doctor":    "Diagnose your system and installation",
	"version":   "Show version information",
}

// parseArgs implements the shared help/error policy for one command:
//
//   - `<cmd> -h` / `--help`: print full usage to stdout, exit 0.
//   - invalid flag: print the error plus full usage to stderr, exit 2.
//
// The returned code is the process exit code when ok is false.
func parseArgs(name string, fs *flag.FlagSet, args []string, stdout, stderr io.Writer) (int, bool) {
	for _, a := range args {
		switch a {
		case "-h", "--help", "-help":
			printCommandHelp(fs, name, stdout)
			return 0, false
		}
	}
	// Parse errors (unknown flags, bad values): usage goes to stderr.
	fs.SetOutput(io.Discard) // flag's auto-print is suppressed; we render it ourselves
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "aseprite-builder %s: %v\n\n", name, err)
		printCommandHelp(fs, name, stderr)
		return 2, false
	}
	return 0, true
}

// printCommandHelp renders the full per-command help block to w.
func printCommandHelp(fs *flag.FlagSet, name string, w io.Writer) {
	fmt.Fprintf(w, "%s\n\nUsage:\n  aseprite-builder %s [flags]\n\nFlags:\n", commandSummaries[name], name)
	flagsBlock := flagLines(fs)
	if len(flagsBlock) == 0 {
		fmt.Fprintln(w, "  (this command takes no flags)")
	}
	for _, line := range flagsBlock {
		fmt.Fprintln(w, "  "+line)
	}
}

// newFlagSet creates a command's FlagSet with silent output; the
// shared parseArgs policy prints (stdout on --help, stderr on errors).
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// flagLines renders a FlagSet's definitions one flag per line, from the
// same definitions the parser uses so help never drifts from behavior.
// Shape: --name <type>   description, descriptions column-aligned.
func flagLines(fs *flag.FlagSet) []string {
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	fs.PrintDefaults()
	fs.SetOutput(io.Discard)

	return alignFlagLines(renderFlagDefaults(buf.String()))
}

// alignFlagLines normalizes descriptions to a shared column.
func alignFlagLines(lines []string) []string {
	width := 0
	var lefts, descs []string
	for _, line := range lines {
		idx := strings.Index(line, "   ")
		if idx < 0 {
			lefts = append(lefts, line)
			descs = append(descs, "")
			continue
		}
		lefts = append(lefts, strings.TrimRight(line[:idx], " "))
		descs = append(descs, line[idx+3:])
		if n := len(lefts[len(lefts)-1]); n > width {
			width = n
		}
	}
	out := make([]string, 0, len(lines))
	for i, left := range lefts {
		out = append(out, padTo(left, width)+"   "+descs[i])
	}
	return out
}

// renderFlagDefaults reformats Go's PrintDefaults output:
//
//	-version string
//	  	pin an exact Aseprite version (e.g. 1.3.18.6)
//
// into "--version string   pin an exact …". Extracted for tests; see
// alignFlagLines for the column alignment pass.
func renderFlagDefaults(defaults string) []string {
	var out []string
	name := ""
	desc := strings.Builder{}
	flush := func() {
		if name == "" {
			return
		}
		line := name + "   " + strings.TrimSpace(desc.String())
		out = append(out, strings.TrimRight(line, " "))
		name = ""
		desc.Reset()
	}
	for _, raw := range strings.Split(defaults, "\n") {
		line := strings.TrimRight(raw, " \t")
		switch {
		case strings.HasPrefix(line, "  -"):
			flush()
			name = "--" + strings.TrimPrefix(strings.TrimSpace(line), "-")
		case strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    "):
			if desc.Len() > 0 {
				desc.WriteString(" ")
			}
			desc.WriteString(strings.TrimSpace(line))
			// Multi-entry description lines stay on one flag line.
		case strings.TrimSpace(line) == "":
			// separator between entries
		}
	}
	flush()
	return out
}

// padTo right-pads s with spaces to n display columns.
func padTo(s string, n int) string {
	gap := n - utf8.RuneCountInString(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}
