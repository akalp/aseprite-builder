package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewFileWrites(t *testing.T) {
	dir := t.TempDir()
	l, path, err := NewFile(dir, "install")
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	defer func() { _ = l.Close() }()

	l.Line("stage %s done", "resolve")
	l.Command("cmake", []string{"-G", "Ninja"}, []string{"PATH=/x"}, "/tmp/work")
	l.CommandResult("cmake", 0, "out line", "err line", 1)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	text := string(data)
	for _, want := range []string{"stage resolve done", "$ cmake -G Ninja", "exit=0", "out line", "err line"} {
		if !strings.Contains(text, want) {
			t.Errorf("log missing %q in:\n%s", want, text)
		}
	}
}

func TestLogPath(t *testing.T) {
	dir := t.TempDir()
	l, path, err := NewFile(dir, "update")
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	defer func() { _ = l.Close() }()
	if filepath.Dir(path) != dir {
		t.Errorf("LogPath dir = %q, want %q", filepath.Dir(path), dir)
	}
	if !strings.HasSuffix(filepath.Base(path), ".log") {
		t.Errorf("log file name = %q, want .log suffix", path)
	}
}

func TestNopLogger(t *testing.T) {
	l := NewNop()
	l.Line("nothing")
	l.Close() // must not panic
	if l.LogPath() != "" {
		t.Error("nop logger should have empty LogPath")
	}
}

func TestRedactEnvKey(t *testing.T) {
	tests := map[string]string{
		"PATH":                  "PATH",
		"HOME":                  "HOME",
		"LANG":                  "LANG",
		"CMAKE_OSX_SYSROOT":     "CMAKE_OSX_SYSROOT",
		"GITHUB_TOKEN":          "GITHUB_TOKEN=(redacted)",
		"GH_TOKEN":              "GH_TOKEN=(redacted)",
		"MY_SECRET_VALUE":       "MY_SECRET_VALUE=(redacted)",
		"password2":             "password2=(redacted)",
		"DOWNLOAD_KEY":          "DOWNLOAD_KEY=(redacted)",
		"AWS_SECRET_ACCESS_KEY": "AWS_SECRET_ACCESS_KEY=(redacted)",
		"AWS_SESSION_TOKEN":     "AWS_SESSION_TOKEN=(redacted)",
		"NPM_TOKEN":             "NPM_TOKEN=(redacted)",
		"DATABASE_PASSWORD":     "DATABASE_PASSWORD=(redacted)",
		"SSH_KEY":               "SSH_KEY=(redacted)",
		"API_KEY":               "API_KEY=(redacted)",
		"CI_JOB_JWT":            "CI_JOB_JWT=(redacted)",
		"NUGET_API_KEY":         "NUGET_API_KEY=(redacted)",
		"SIGNING_CREDENTIALS":   "SIGNING_CREDENTIALS=(redacted)",
	}
	for in, want := range tests {
		if got := redactEnvKey(in); got != want {
			t.Errorf("redactEnvKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactValue(t *testing.T) {
	if got := RedactValue("supersecret"); got != "su****" {
		t.Errorf("RedactValue = %q", got)
	}
	if got := RedactValue(""); got != "" {
		t.Errorf("RedactValue(\"\") = %q", got)
	}
	if got := RedactValue("ab"); got != "****" {
		t.Errorf("RedactValue short = %q", got)
	}
}

func TestIndentedBlockStderrFirstLineOnly(t *testing.T) {
	dir := t.TempDir()
	l, _, err := NewFile(dir, "block")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	// Capture stderr
	l.IndentedBlock("stdout", "line1\nline2\nline3")
	// File must contain all lines
	data, err := os.ReadFile(filepath.Join(dir, mustLogName(t, dir)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "line2") {
		t.Error("full block should be in the file")
	}
}

func mustLogName(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no log file created")
	}
	return entries[0].Name()
}
