package aseprite

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Build progress phases for the "Compiling Aseprite" stage. The first
// phase covers everything the upstream script does before its first
// ninja rule (Skia checks, cmake configure); the second is the
// incremental compile itself, reported with done/total counters parsed
// from ninja's "[n/m]" progress lines.
const (
	BuildPhaseConfigure = "configure"
	BuildPhaseCompile   = "compile"
)

// BuildProgress is one progress tick of the upstream build.
type BuildProgress struct {
	// Phase is BuildPhaseConfigure or BuildPhaseCompile.
	Phase string
	// Done/Total are the ninja rule counters (0/0 empty during
	// configure).
	Done  int64
	Total int64
}

// BuildProgressSink receives build progress ticks.
type BuildProgressSink func(BuildProgress)

// ninjaProgressRe matches ninja rule progress lines like "[123/4567] Building
// CXX object ...".
var ninjaProgressRe = regexp.MustCompile(`\[\s*(\d+)\s*/\s*(\d+)\s*\]`)

// newProgressTracker watches build output and reports progress. It is
// safe to feed non-line-terminated chunks (partial writes).
func newProgressTracker(sink BuildProgressSink) io.Writer {
	return &progressTracker{sink: sink}
}

type progressTracker struct {
	sink  BuildProgressSink
	carry []byte
}

// Write implements io.Writer, accumulating partial lines and handing
// complete ones to the parser.
func (t *progressTracker) Write(p []byte) (int, error) {
	n := len(p)
	t.carry = append(t.carry, p...)
	for {
		idx := bytes.IndexByte(t.carry, '\n')
		if idx < 0 {
			break
		}
		line := string(t.carry[:idx])
		t.carry = t.carry[idx+1:]
		t.parseLine(line)
	}
	return n, nil
}

func (t *progressTracker) parseLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	m := ninjaProgressRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	done, err1 := strconv.ParseInt(m[1], 10, 64)
	total, err2 := strconv.ParseInt(m[2], 10, 64)
	if err1 != nil || err2 != nil {
		return
	}
	if t.sink != nil && total > 0 {
		t.sink(BuildProgress{Phase: BuildPhaseCompile, Done: done, Total: total})
	}
}
