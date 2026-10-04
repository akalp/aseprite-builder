package aseprite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/runner"
)

const gitTestCommit = "abc1234def5678901234567890abcdef12345678"

func TestReleaseDevHelpers(t *testing.T) {
	dev := Release{Commit: gitTestCommit}
	if !dev.DevCommit() {
		t.Error("DevCommit() = false for a commit release")
	}
	if got := dev.BuildVersion(); got != "dev-abc1234" {
		t.Errorf("BuildVersion() = %q", got)
	}
	if !strings.Contains(dev.String(), "development commit abc1234") {
		t.Errorf("String() = %q", dev.String())
	}

	stable := Release{Version: mustVersion(t, "1.3.18.6"), TagName: "v1.3.18.6"}
	if stable.DevCommit() {
		t.Error("DevCommit() = true for a stable release")
	}
	if got := stable.BuildVersion(); got != "1.3.18.6" {
		t.Errorf("BuildVersion() = %q", got)
	}
	if !strings.Contains(stable.String(), "1.3.18.6") {
		t.Errorf("String() = %q", stable.String())
	}
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestValidCommit(t *testing.T) {
	valid := []string{
		"abc1234",
		gitTestCommit,
		"ABC1234DEF5678901234567890ABCDEF12345678",
	}
	for _, s := range valid {
		if !ValidCommit(s) {
			t.Errorf("ValidCommit(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",
		"abc123",                // too short
		strings.Repeat("a", 41), // too long
		"abc1234g",              // non-hex
		"abc 1234",              // whitespace
	}
	for _, s := range invalid {
		if ValidCommit(s) {
			t.Errorf("ValidCommit(%q) = true, want false", s)
		}
	}
}

func TestGitCommitSourceInvalidCommit(t *testing.T) {
	src := GitCommitSource{Runner: &runner.FakeRunner{}}
	err := src.Ensure(context.Background(), "xyz", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "invalid development commit") {
		t.Fatalf("err = %v, want an invalid-commit error", err)
	}
}

func TestGitCommitSourceRequiresGit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aseprite-dev-abc1234")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "keep-me")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &runner.FakeRunner{Errs: map[string]error{
		"git --version": errors.New("exec: git: executable file not found in $PATH"),
	}}
	src := GitCommitSource{Runner: f}
	err := src.Ensure(context.Background(), gitTestCommit, dir)
	if !errors.Is(err, ErrGitRequired) {
		t.Fatalf("err = %v, want ErrGitRequired", err)
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Error("a missing Git must never delete an existing checkout")
	}
}

func TestGitCommitSourceReusesMatchingCheckout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aseprite-dev-abc1234")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "CMakeLists.txt")
	if err := os.WriteFile(sentinel, []byte("cmake"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &runner.FakeRunner{Results: map[string]runner.Result{
		"git --version":                     {Stdout: "git version 2.47.0\n"},
		"git -C " + dir + " rev-parse HEAD": {Stdout: gitTestCommit + "\n"},
	}}
	src := GitCommitSource{Runner: f}
	// An abbreviated commit still matches the full HEAD.
	if err := src.Ensure(context.Background(), gitTestCommit[:8], dir); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Error("a matching checkout must be left untouched")
	}
	for _, c := range f.Called() {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "clone" {
			t.Errorf("matching checkout must not be cloned again: %v", c)
		}
	}
}

func TestGitCommitSourceFreshCheckout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sources", "aseprite-dev-abc1234")
	f := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if c.Name != "git" {
			return runner.Result{}, false
		}
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			return runner.Result{Stdout: "git version 2.47.0\n"}, true
		}
		// rev-parse finds nothing (empty output → fresh checkout), all
		// other git commands succeed.
		return runner.Result{ExitCode: 0}, true
	}}
	src := GitCommitSource{Runner: f}
	if err := src.Ensure(context.Background(), gitTestCommit, dir); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	var seq []string
	for _, c := range f.Called() {
		seq = append(seq, strings.Join(c.Args, " "))
	}
	want := []string{
		"--version",
		"-C " + dir + " rev-parse HEAD",
		"clone --filter=blob:none --no-checkout " + OfficialRepoURL + " " + dir,
		"-C " + dir + " checkout --detach " + gitTestCommit,
		"-C " + dir + " submodule update --init --recursive",
	}
	if len(seq) != len(want) {
		t.Fatalf("git calls = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Errorf("git call %d = %q, want %q", i, seq[i], want[i])
		}
	}
}

func TestGitCommitSourceCloneFailureNamesGitOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aseprite-dev-abc1234")
	cloneKey := "git clone --filter=blob:none --no-checkout " + OfficialRepoURL + " " + dir
	f := &runner.FakeRunner{Errs: map[string]error{
		cloneKey: runner.ErrExit{ExitCode: 128, Cmd: cloneKey, Stderr: "fatal: repository not found"},
	}}
	src := GitCommitSource{Runner: f}
	err := src.Ensure(context.Background(), gitTestCommit, dir)
	if err == nil || !strings.Contains(err.Error(), "repository not found") {
		t.Fatalf("err = %v, want git's stderr in the message", err)
	}
	if errors.Is(err, ErrGitRequired) {
		t.Error("a clone failure is not a missing-Git error")
	}
}

func TestGitCommitSourceCustomURL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aseprite-dev-abc1234")
	f := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if c.Name == "git" {
			return runner.Result{ExitCode: 0}, true
		}
		return runner.Result{}, false
	}}
	src := GitCommitSource{Runner: f, URL: "https://example.invalid/mirror.git"}
	if err := src.Ensure(context.Background(), gitTestCommit, dir); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	var clone string
	for _, c := range f.Called() {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "clone" {
			clone = strings.Join(c.Args, " ")
		}
	}
	if !strings.Contains(clone, "https://example.invalid/mirror.git") {
		t.Errorf("clone = %q, want the override URL", clone)
	}
}
