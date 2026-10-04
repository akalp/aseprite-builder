// Package runner provides a single, replaceable way to launch external
// processes so callers can log, test against fakes, and never shell out
// via string parsing.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Command describes a single process invocation with its environment
// overrides. Env entries use "KEY=value"; nil entries must not be used.
type Command struct {
	// Name is the executable path (absolute or found on PATH).
	Name string
	// Args are the arguments passed verbatim to the executable.
	Args []string
	// Env contains only explicit "KEY=value" overrides; the parent
	// environment is inherited by default.
	Env []string
	// Dir is the working directory. If empty the parent's is inherited.
	Dir string
	// Stdin optionally supplies bytes for the process's stdin.
	Stdin []byte
	// Interactive, when true, connects the child directly to the
	// caller's stdin/stdout/stderr instead of capturing output. Used
	// for privileged package installation the user explicitly
	// consented to watch (spec §16: never silently invoke sudo, never
	// strip the terminal). Takes precedence over Stdin.
	Interactive bool
}

// String is used for correlation keys and logging.
func (c Command) String() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}

// Result captures the outcome of a process invocation.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// OK reports whether the process terminated cleanly.
func (r Result) OK() bool { return r.ExitCode == 0 }

// Combined returns output in a stable, log-friendly order.
func (r Result) Combined() string {
	var b strings.Builder
	b.WriteString(r.Stdout)
	if r.Stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(r.Stderr)
	}
	return b.String()
}

// ErrExit wraps a non-zero exit code so callers can check with errors.As.
type ErrExit struct {
	ExitCode int
	Cmd      string
	Stdout   string
	Stderr   string
}

func (e ErrExit) Error() string {
	return fmt.Sprintf("%s exited with code %d", e.Cmd, e.ExitCode)
}

// Runner launches commands and returns their outcome.
//
// Implementations may also implement io.Writer-style streaming hooks or
// richer result types; see ConcreteRunner for the system implementation
// and runner package tests for the in-memory fake.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// Detacher is an optional Runner capability: start a command without
// waiting for it to exit. The builder uses it to launch installed GUI
// applications so the CLI/TUI keep running after the app starts.
// Callers type-assert; FakeRunner-based tests simply observe Run calls.
type Detacher interface {
	StartDetached(ctx context.Context, cmd Command) error
}

// ConcreteRunner is the default Runner, spawning real OS processes.
// Stream optionally receives live combined stdout/stderr; captured
// output is always returned in the Result regardless.
type ConcreteRunner struct {
	// Stream, when non-nil, receives process output as it happens.
	Stream io.Writer
}

// NewConcreteRunner returns the default system Runner.
func NewConcreteRunner() ConcreteRunner { return ConcreteRunner{} }

// Run implements Runner using exec.CommandContext. Output is always
// captured; when Stream is set it is also forwarded live. Interactive
// commands own the terminal and are therefore not captured.
func (r ConcreteRunner) Run(ctx context.Context, cmd Command) (Result, error) {
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...) //nolint:gosec // arguments come from trusted app logic
	if cmd.Dir != "" {
		c.Dir = cmd.Dir
	}
	if len(cmd.Env) > 0 {
		c.Env = append(os.Environ(), cmd.Env...)
	}
	if cmd.Interactive {
		return runInteractive(c, cmd)
	}
	var stdout, stderr bytes.Buffer
	if r.Stream != nil {
		c.Stdout = io.MultiWriter(&stdout, r.Stream)
		c.Stderr = io.MultiWriter(&stderr, r.Stream)
	} else {
		c.Stdout = &stdout
		c.Stderr = &stderr
	}
	if len(cmd.Stdin) > 0 {
		c.Stdin = bytes.NewReader(cmd.Stdin)
	}
	start := time.Now()
	if err := c.Start(); err != nil {
		return Result{}, fmt.Errorf("start %s: %w", cmd.Name, err)
	}
	err := c.Wait()
	duration := time.Since(start)
	if exitErr, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec.ExitError is precise enough here
		code := exitErr.ExitCode()
		return Result{
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
			ExitCode: code,
			Duration: duration,
		}, ErrExit{
			ExitCode: code,
			Cmd:      cmd.String(),
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
		}
	}
	if err != nil {
		return Result{}, fmt.Errorf("run %s: %w", cmd.Name, err)
	}
	return Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: duration,
	}, nil
}

// runInteractive executes cmd with the caller's terminal attached and
// returns only the exit outcome (output is not captured — the user is
// watching it live).
func runInteractive(c *exec.Cmd, cmd Command) (Result, error) {
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	start := time.Now()
	err := c.Run()
	duration := time.Since(start)
	if exitErr, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec.ExitError is precise enough here
		code := exitErr.ExitCode()
		return Result{ExitCode: code, Duration: duration},
			ErrExit{ExitCode: code, Cmd: cmd.String()}
	}
	if err != nil {
		return Result{}, fmt.Errorf("run %s: %w", cmd.Name, err)
	}
	return Result{Duration: duration}, nil
}

// StartDetached implements Detacher: the process starts with stdio on
// the null device and is released, so its lifetime is independent of
// the caller. ctx is only consulted before starting — a cancelled
// install must not kill the application it just launched.
func (r ConcreteRunner) StartDetached(ctx context.Context, cmd Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := exec.Command(cmd.Name, cmd.Args...) //nolint:gosec // arguments come from trusted app logic
	if cmd.Dir != "" {
		c.Dir = cmd.Dir
	}
	if len(cmd.Env) > 0 {
		c.Env = append(os.Environ(), cmd.Env...)
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	c.Stdin, c.Stdout, c.Stderr = devNull, devNull, devNull
	if err := c.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd.Name, err)
	}
	return c.Process.Release()
}

// NopRunner is a Runner that accepts any command and returns a fixed
// Result without executing anything. Handy for tests that want to
// observe which commands a subsystem *would* run.
type NopRunner struct{}

// Run implements Runner vacuously.
func (NopRunner) Run(context.Context, Command) (Result, error) {
	return Result{}, nil
}

// FakeRunner captures commands and serves canned results, letting tests
// assert on the exact commands issued by business logic without ever
// spawning a real process.
type FakeRunner struct {
	mu      sync.Mutex
	Calls   []Command
	Results map[string]Result
	// Errs maps cmd.String() to an error to return in place of Result.
	Errs map[string]error
	// ErrMatch, when non-nil, is consulted before Results/Match and may
	// return an error to surface for this command (pattern-based error
	// injection).
	ErrMatch func(cmd Command) (error, bool)
	// Match, when non-nil, is consulted after the exact-key lookups and
	// may return a result for pattern-based matching (e.g. any
	// `--version` invocation regardless of path).
	Match func(cmd Command) (Result, bool)
	// Default result if not matched in Results.
	Default Result
}

// Run records the call and returns the canned Result or error.
func (f *FakeRunner) Run(_ context.Context, cmd Command) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, cmd)
	if err, ok := f.Errs[cmd.String()]; ok {
		return f.Default, err
	}
	if f.ErrMatch != nil {
		if err, ok := f.ErrMatch(cmd); ok {
			return f.Default, err
		}
	}
	if res, ok := f.Results[cmd.String()]; ok {
		return res, nil
	}
	if f.Match != nil {
		if res, ok := f.Match(cmd); ok {
			return res, nil
		}
	}
	return f.Default, nil
}

// CalledStrings returns recorded command names, convenient for
// assertions like expected := []string{"cmake", "ninja"}.
func (f *FakeRunner) CalledStrings() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Calls))
	for _, c := range f.Calls {
		out = append(out, c.Name)
	}
	return out
}

// Called returns a copy of the recorded calls.
func (f *FakeRunner) Called() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Command(nil), f.Calls...)
}

// Reset clears recorded calls so a single FakeRunner can drive many
// assertions.
func (f *FakeRunner) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = nil
}
