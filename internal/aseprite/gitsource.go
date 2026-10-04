package aseprite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// OfficialRepoURL is the official Aseprite git repository used by the
// development channel. Stable releases never need Git; this URL is only
// consulted when the user explicitly asks for a development commit
// (spec §14).
const OfficialRepoURL = "https://github.com/aseprite/aseprite.git"

// ErrGitRequired reports that the development channel needs Git and it
// is not available on this machine.
var ErrGitRequired = errors.New("the development channel requires Git, which was not found")

// GitCommitSource materializes an explicit development-channel commit
// into a buildable source tree. It never downloads release archives and
// only ever writes inside the builder-managed source directory it is
// given.
type GitCommitSource struct {
	// Runner executes git (required).
	Runner runner.Runner
	// URL overrides the repository ("" → OfficialRepoURL).
	URL string
	// Log receives decision notes (nil disables).
	Log func(format string, args ...any)
}

// Ensure checks out commit into dir, reusing an existing checkout whose
// HEAD already matches. dir is builder-owned: a mismatched or broken
// checkout is replaced, never merged into.
func (g GitCommitSource) Ensure(ctx context.Context, commit, dir string) error {
	if !ValidCommit(commit) {
		return fmt.Errorf("invalid development commit %q: expected 7-40 hex characters", commit)
	}
	if g.Runner == nil {
		return errors.New("git source: runner is not set")
	}
	logf := g.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// Probe Git before touching anything: a missing Git must never
	// delete a previously working checkout.
	if err := g.checkGit(ctx); err != nil {
		return err
	}

	if head, err := g.head(ctx, dir); err == nil && commitMatches(commit, head) {
		logf("development checkout reused at %s (HEAD %s)", dir, ShortCommit(head))
		return nil
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear development source dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("prepare development source parent: %w", err)
	}

	url := g.URL
	if url == "" {
		url = OfficialRepoURL
	}
	logf("checking out %s at commit %s", url, ShortCommit(commit))

	// A blob-filtered clone keeps the download small; the checkout
	// fetches exactly the blobs of the requested commit. Submodules are
	// required: the official release archive vendors them, a git
	// checkout must initialize them.
	steps := [][]string{
		{"clone", "--filter=blob:none", "--no-checkout", url, dir},
		{"-C", dir, "checkout", "--detach", commit},
		{"-C", dir, "submodule", "update", "--init", "--recursive"},
	}
	for _, args := range steps {
		if err := g.run(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

// commitMatches reports whether head (full SHA) matches commit, which
// may be an abbreviated SHA.
func commitMatches(commit, head string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(head)), strings.ToLower(strings.TrimSpace(commit)))
}

// checkGit fails with ErrGitRequired when git cannot be started.
func (g GitCommitSource) checkGit(ctx context.Context) error {
	if _, err := g.Runner.Run(ctx, runner.Command{Name: "git", Args: []string{"--version"}}); err != nil {
		var exitErr runner.ErrExit
		if !errors.As(err, &exitErr) {
			return fmt.Errorf("%w: %v", ErrGitRequired, err)
		}
		// Git ran but reported an error; unusual, but treat it as
		// unusable rather than pretending it is missing.
		return fmt.Errorf("git is not usable: %w", err)
	}
	return nil
}

// head returns the checkout's current HEAD SHA, or an error when dir is
// not a usable git checkout.
func (g GitCommitSource) head(ctx context.Context, dir string) (string, error) {
	res, err := g.Runner.Run(ctx, runner.Command{Name: "git", Args: []string{"-C", dir, "rev-parse", "HEAD"}})
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	if !res.OK() {
		return "", fmt.Errorf("git rev-parse HEAD exited with code %d", res.ExitCode)
	}
	head := strings.TrimSpace(res.Stdout)
	if head == "" {
		return "", errors.New("git rev-parse HEAD produced no output")
	}
	return head, nil
}

func (g GitCommitSource) run(ctx context.Context, args ...string) error {
	res, err := g.Runner.Run(ctx, runner.Command{Name: "git", Args: args})
	var exitErr runner.ErrExit
	switch {
	case errors.As(err, &exitErr):
		tail := strings.TrimSpace(exitErr.Stderr)
		if tail == "" {
			tail = strings.TrimSpace(exitErr.Stdout)
		}
		if tail != "" {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, lastLine(tail))
		}
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	case err != nil:
		return fmt.Errorf("%w: %v", ErrGitRequired, err)
	case !res.OK():
		return fmt.Errorf("git %s exited with code %d", strings.Join(args, " "), res.ExitCode)
	}
	return nil
}

// lastLine keeps git errors readable.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
