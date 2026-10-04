package runner

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestFakeRunnerRecordsCalls(t *testing.T) {
	f := &FakeRunner{Results: map[string]Result{
		"cmake --version": {Stdout: "cmake version 3.31.6", ExitCode: 0},
	}}
	res, err := f.Run(context.Background(), Command{
		Name: "cmake",
		Args: []string{"--version"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "3.31.6") {
		t.Errorf("stdout = %q, want version", res.Stdout)
	}
	if got, want := len(f.Calls), 1; got != want {
		t.Fatalf("calls = %d, want %d", got, want)
	}
	if name := f.CalledStrings(); len(name) != 1 || name[0] != "cmake" {
		t.Errorf("CalledStrings = %v", name)
	}
}

func TestFakeRunnerErrors(t *testing.T) {
	sentinel := errors.New("boom")
	f := &FakeRunner{Errs: map[string]error{"false": sentinel}}
	res, err := f.Run(context.Background(), Command{Name: "false"})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("default result exit code = %d", res.ExitCode)
	}
}

func TestCommandLineJoin(t *testing.T) {
	got := Command{Name: "cmake", Args: []string{"-G", "Ninja", ".."}}.String()
	want := "cmake -G Ninja .."
	if got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestCommandStringNoArgs(t *testing.T) {
	cmd := Command{Name: "ninja"}
	if got := cmd.String(); got != "ninja" {
		t.Errorf("String = %q, want %q", got, "ninja")
	}
}

func TestResultOK(t *testing.T) {
	if !(Result{ExitCode: 0}).OK() {
		t.Error("zero exit should be OK")
	}
	if (Result{ExitCode: 1}).OK() {
		t.Error("nonzero exit should not be OK")
	}
}

func TestResultCombined(t *testing.T) {
	r := Result{Stdout: "out", Stderr: "err"}
	if got, want := r.Combined(), "out\nerr"; got != want {
		t.Errorf("Combined = %q, want %q", got, want)
	}
	if got := (Result{Stdout: "only"}).Combined(); got != "only" {
		t.Errorf("stdout-only Combined = %q", got)
	}
}

func TestNewConcreteRunnerExists(t *testing.T) {
	// Sanity: constructor returns a usable Runner without touching the disk.
	var _ Runner = NewConcreteRunner()
}

func TestConcreteRunnerInteractiveExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("interactive smoke test uses /bin/sh")
	}
	res, err := (ConcreteRunner{}).Run(context.Background(), Command{
		Name:        "/bin/sh",
		Args:        []string{"-c", "exit 3"},
		Interactive: true,
	})
	var exitErr ErrExit
	if !errors.As(err, &exitErr) || exitErr.ExitCode != 3 {
		t.Fatalf("err = %v, want ErrExit with code 3", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("result exit = %d, want 3", res.ExitCode)
	}
	if res.Stdout != "" || res.Stderr != "" {
		t.Errorf("interactive output must not be captured: %+v", res)
	}
}

func TestConcreteRunnerStartDetached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detached smoke test uses /bin/sh")
	}
	var d Detacher = ConcreteRunner{}
	if err := d.StartDetached(context.Background(), Command{Name: "/bin/sh", Args: []string{"-c", "true"}}); err != nil {
		t.Fatalf("StartDetached: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.StartDetached(ctx, Command{Name: "/bin/sh"}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx err = %v, want context.Canceled", err)
	}
}
