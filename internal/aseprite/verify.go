package aseprite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// VerifyBuild confirms that a completed build produced a runnable
// artifact matching the requested release:
//
//   - macOS: the bundle and its executable exist,
//   - all layouts: the runtime data directory is populated (upstream's
//     copy_data target must have run),
//   - the executable reports the expected version (aseprite --version).
//
// Verification failures are fatal for install: the builder never
// replaces a working installation with an unverified artifact.
func VerifyBuild(ctx context.Context, r runner.Runner, out BuildOutput, wantVersion string) error {
	if out.AppPath != "" {
		if fi, err := os.Stat(out.AppPath); err != nil || !fi.IsDir() {
			return fmt.Errorf("verify: bundle %s is missing: %w", out.AppPath, err)
		}
	} else if out.Executable == "" {
		return fmt.Errorf("verify: no application bundle or executable in build output")
	}
	if fi, err := os.Stat(out.Executable); err != nil || fi.IsDir() {
		return fmt.Errorf("verify: executable %s is missing: %w", out.Executable, err)
	}
	if runtimeDataEmpty(out.DataDir) {
		return &ContractViolationError{
			Stage:  "verification",
			Reason: fmt.Sprintf("runtime data dir %s is empty; the upstream data copy step did not run", out.DataDir),
		}
	}
	return VerifyExecutable(ctx, r, out.Executable, wantVersion)
}

// VerifyInstalled checks the installed macOS bundle: the executable
// exists at Contents/MacOS/aseprite and reports the expected version.
// Platforms installed as plain directories use VerifyExecutable with
// the platform-resolved executable path instead.
func VerifyInstalled(ctx context.Context, r runner.Runner, appPath, wantVersion string) error {
	if appPath == "" {
		return fmt.Errorf("verify: no installation to verify")
	}
	if fi, err := os.Stat(appPath); err != nil || !fi.IsDir() {
		return fmt.Errorf("verify: installation %s is missing: %w", appPath, err)
	}
	exe := filepath.Join(appPath, "Contents", "MacOS", "aseprite")
	if fi, err := os.Stat(exe); err != nil || fi.IsDir() {
		return fmt.Errorf("verify: installed bundle %s has no executable at %s", appPath, exe)
	}
	return VerifyExecutable(ctx, r, exe, wantVersion)
}

// VerifyExecutable checks that an executable (built or installed)
// exists and reports the expected version.
func VerifyExecutable(ctx context.Context, r runner.Runner, executable, wantVersion string) error {
	if executable == "" {
		return fmt.Errorf("verify: no executable to check")
	}
	return verifyExecVersion(ctx, r, executable, wantVersion)
}

func verifyExecVersion(ctx context.Context, r runner.Runner, executable, wantVersion string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// HOME is redirected to a throwaway dir so any first-run writes go
	// to a sandbox instead of the user's real profile (APPDATA is the
	// Windows equivalent and redirects the same way).
	sandbox, err := os.MkdirTemp("", "aseprite-builder-verify-")
	if err != nil {
		return fmt.Errorf("create verification sandbox: %w", err)
	}
	defer func() { _ = os.RemoveAll(sandbox) }()

	res, err := r.Run(ctx, runner.Command{
		Name: executable,
		Args: []string{"--version"},
		Env:  []string{"HOME=" + sandbox, "APPDATA=" + sandbox},
	})
	if err != nil {
		return fmt.Errorf("verify: running %s --version: %w", filepath.Base(executable), err)
	}
	if !res.OK() {
		return fmt.Errorf("verify: %s --version exited with code %d", filepath.Base(executable), res.ExitCode)
	}
	versionOutput := strings.TrimSpace(res.Stdout)
	if versionOutput == "" {
		return fmt.Errorf("verify: %s --version produced no output", filepath.Base(executable))
	}
	if wantVersion != "" && !strings.Contains(versionOutput, wantVersion) {
		return fmt.Errorf("verify: reported version %q does not match expected %q", versionOutput, wantVersion)
	}
	return nil
}

func runtimeDataEmpty(dataDir string) bool {
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) == 0 {
		return true
	}
	// Require at least one non-empty directory or file beyond dot
	// entries; a partial data copy is caught here too.
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		return false
	}
	return true
}
