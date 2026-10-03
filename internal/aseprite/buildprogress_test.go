package aseprite

import (
	"path/filepath"
	"testing"

	"github.com/akalp/aseprite-builder/internal/runner"
)

func collect(sink *[]BuildProgress) BuildProgressSink {
	return func(p BuildProgress) { *sink = append(*sink, p) }
}

func TestProgressTrackerNinjaLines(t *testing.T) {
	var events []BuildProgress
	tr := newProgressTracker(collect(&events))
	_, _ = tr.Write([]byte("[   0/1470] CXX object foo.o\n[10/1470] Linking bar\n[1470/1470] done\n"))
	if len(events) != 3 {
		t.Fatalf("events = %+v, want 3", events)
	}
	if events[0].Phase != BuildPhaseCompile || events[0].Done != 0 || events[0].Total != 1470 {
		t.Errorf("e0 = %+v", events[0])
	}
	if events[2].Done != 1470 {
		t.Errorf("e2 = %+v", events[2])
	}
}

func TestProgressTrackerPartialWrites(t *testing.T) {
	var events []BuildProgress
	tr := newProgressTracker(collect(&events))
	// One ninja line split across three writes.
	_, _ = tr.Write([]byte("[5/"))
	_, _ = tr.Write([]byte("12042] Building CXX ob"))
	_, _ = tr.Write([]byte("ject src/main.cpp.o\n"))
	if len(events) != 1 || events[0].Done != 5 || events[0].Total != 12042 {
		t.Fatalf("events = %+v", events)
	}
}

func TestProgressTrackerIgnoresNonProgressLines(t *testing.T) {
	var events []BuildProgress
	tr := newProgressTracker(collect(&events))
	_, _ = tr.Write([]byte("-- Configuring done\n-- Generating done\nCMake Error something [garbage]\n\n"))
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}

func TestProgressTrackerTotalsGrowth(t *testing.T) {
	var events []BuildProgress
	tr := newProgressTracker(collect(&events))
	_, _ = tr.Write([]byte("[1/100] a\n"))
	_, _ = tr.Write([]byte("[100/150] b\n"))
	if events[0].Total != 100 || events[1].Total != 150 {
		t.Fatalf("events = %+v", events)
	}
}

func TestBuilderAnnouncesConfigurePhase(t *testing.T) {
	src := fixtureSource(t)
	var events []BuildProgress
	r := &runner.FakeRunner{Results: map[string]runner.Result{
		buildScriptKey(src): {ExitCode: 1}, // fail early; progress already announced
	}}
	b := Builder{Runner: r, Progress: collect(&events)}
	_, _ = b.Build(t.Context(), BuildRequest{
		SourceDir: src,
		Contract:  Contract{BuildScript: filepath.Join(src, "build.sh")},
	})
	if len(events) == 0 || events[0] != (BuildProgress{Phase: BuildPhaseConfigure}) {
		t.Fatalf("events = %+v, want configure announcement first", events)
	}
}

func TestPercentParsingHelpers(t *testing.T) {
	// parseLine dedup behavior covered above; ensure the regex is strict.
	for _, line := range []string{"[n/m] x", "[1/0] y", "23%"} {
		var events []BuildProgress
		tr := newProgressTracker(collect(&events))
		_, _ = tr.Write([]byte(line + "\n"))
		if len(events) != 0 {
			t.Errorf("line %q produced events %+v", line, events)
		}
	}
}
