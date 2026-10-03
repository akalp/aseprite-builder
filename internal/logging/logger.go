// Package logging provides per-operation log files plus a stderr
// progress printer, with deliberate redaction of credential-shaped
// environment values so logs stay shareable.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger writes timestamped lines to an operation log file and mirrors
// them to stderr. All exported methods are safe for concurrent use.
type Logger struct {
	mu     sync.Mutex
	file   *os.File
	stderr io.Writer
}

// NewFile creates a timestamped log file under dir, mirroring output to
// stderr.
func NewFile(dir, op string) (*Logger, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("create log dir: %w", err)
	}
	name := fmt.Sprintf("%s-%s.log", op, time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644) //nolint:gosec // path built from trusted constants
	if err != nil {
		return nil, "", fmt.Errorf("create log file: %w", err)
	}
	return &Logger{file: f, stderr: os.Stderr}, path, nil
}

// NewNop returns a logger that discards everything; useful in tests.
func NewNop() *Logger {
	return &Logger{stderr: io.Discard}
}

// Line writes one redacted line to the log and mirrors to stderr.
func (l *Logger) Line(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		fmt.Fprintf(l.file, "%s %s\n", time.Now().UTC().Format("15:04:05"), msg)
	}
	if l.stderr != nil {
		fmt.Fprintln(l.stderr, msg)
	}
}

// Command records a command invocation in a stable, greppable format.
func (l *Logger) Command(name string, args []string, env []string, dir string) {
	l.Line("$ %s %s", name, strings.Join(args, " "))
	if dir != "" {
		l.Line("  (in %s)", dir)
	}
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok {
			l.Line("  env %s", redactEnvKey(k))
		}
	}
}

// CommandResult records an outcome with exit code and captured output.
func (l *Logger) CommandResult(name string, exitCode int, stdout, stderr string, duration time.Duration) {
	l.Line("=> %s exit=%d duration=%s", name, exitCode, duration)
	if stdout != "" {
		l.IndentedBlock("stdout", stdout)
	}
	if stderr != "" {
		l.IndentedBlock("stderr", stderr)
	}
}

// IndentedBlock writes a multi-line block with a fixed prefix.
func (l *Logger) IndentedBlock(label, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if l.file != nil {
			fmt.Fprintf(l.file, "  %s: %s\n", label, line)
		}
	}
	if l.stderr != nil {
		// Only the first line goes to stderr to keep the CLI readable;
		// full output lives in the file.
		first := strings.SplitN(strings.TrimRight(text, "\n"), "\n", 2)[0]
		fmt.Fprintf(l.stderr, "  %s: %s\n", label, first)
	}
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// fileWriter adapts the logger's file into an io.Writer for process
// streaming, guarding writes with the logger mutex.
type fileWriter struct{ l *Logger }

func (w fileWriter) Write(p []byte) (int, error) {
	w.l.mu.Lock()
	defer w.l.mu.Unlock()
	if w.l.file == nil {
		return len(p), nil
	}
	return w.l.file.Write(p)
}

// Writer returns an io.Writer that appends raw bytes (process output)
// to the log file. Use together with runner.ConcreteRunner.Stream.
func (l *Logger) Writer() io.Writer { return fileWriter{l} }

// LogPath returns the backing file path, or "" for nop loggers.
func (l *Logger) LogPath() string {
	if l.file == nil {
		return ""
	}
	return l.file.Name()
}

// redactEnvKey marks sensitive environment keys so raw values never hit
// the log. The builder only ever passes its own build env, but the log
// layer enforces the invariant defense-in-depth.
func redactEnvKey(key string) string {
	upper := strings.ToUpper(key)
	for _, sensitive := range []string{"TOKEN", "PASSWORD", "PASSWD", "SECRET", "KEY", "CREDENTIAL"} {
		if strings.Contains(upper, sensitive) {
			return key + "=(redacted)"
		}
	}
	return key
}

// RedactValue masks a potentially sensitive string value in logs.
func RedactValue(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 4 {
		return "****"
	}
	return v[:2] + "****"
}
