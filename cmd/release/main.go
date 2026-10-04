// Command release cross-compiles and packs the four spec §3 release
// artifacts into dist/, then runs the §38 release-safety checker over
// them. It is driven by `make release` and vendors nothing: only the
// local Go toolchain is used.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akalp/aseprite-builder/internal/release"
	"github.com/akalp/aseprite-builder/internal/releasecheck"
	"github.com/akalp/aseprite-builder/internal/runner"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	dist := fs.String("dist", "dist", "output directory for the artifacts")
	onlyOS := fs.String("os", "", "build only this GOOS (default: every target)")
	onlyArch := fs.String("arch", "", "build only this GOARCH (default: every architecture)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	r := runner.NewConcreteRunner()
	version := release.Version(ctx, r, os.Getenv)

	// dist/ is owned by this target; stale artifacts must never mix
	// into a release (make clean removes it too).
	if err := os.RemoveAll(*dist); err != nil {
		return fmt.Errorf("reset %s: %w", *dist, err)
	}
	if err := os.MkdirAll(*dist, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", *dist, err)
	}

	fmt.Printf("aseprite-builder %s → %s/\n", version, *dist)
	var paths []string
	for _, t := range release.Targets() {
		if *onlyOS != "" && t.OS != *onlyOS {
			continue
		}
		if *onlyArch != "" && t.Arch != *onlyArch {
			continue
		}
		tmp := filepath.Join(*dist, "."+t.Artifact+".bin")
		cmd := runner.Command{
			Name: "go",
			Args: []string{
				"build", "-trimpath",
				"-ldflags", "-X main.version=" + version,
				"-o", tmp,
				"./cmd/aseprite-builder",
			},
			Env: []string{"CGO_ENABLED=0", "GOOS=" + t.OS, "GOARCH=" + t.Arch},
		}
		if _, err := r.Run(ctx, cmd); err != nil {
			var exitErr runner.ErrExit
			if errors.As(err, &exitErr) && exitErr.Stderr != "" {
				return fmt.Errorf("build %s/%s: %w: %s", t.OS, t.Arch, err, strings.TrimSpace(exitErr.Stderr))
			}
			return fmt.Errorf("build %s/%s: %w", t.OS, t.Arch, err)
		}
		binary, err := os.ReadFile(tmp) //nolint:gosec // path from trusted release logic
		if err != nil {
			return fmt.Errorf("read built binary: %w", err)
		}
		out := filepath.Join(*dist, t.Artifact)
		if err := release.Pack(out, binary, t); err != nil {
			return err
		}
		if err := os.Remove(tmp); err != nil {
			return fmt.Errorf("remove temp binary: %w", err)
		}
		paths = append(paths, out)
		fmt.Printf("  ✓ %s (%.1f MiB)\n", t.Artifact, float64(len(binary))/(1<<20))
	}
	if len(paths) == 0 {
		return fmt.Errorf("no release targets match -os=%q -arch=%q", *onlyOS, *onlyArch)
	}

	violations, err := releasecheck.CheckPaths(paths)
	if err != nil {
		return err
	}
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "  ✗ %s\n", v)
	}
	if len(violations) > 0 {
		return fmt.Errorf("release-safety check failed (spec §38): %d violation(s)", len(violations))
	}
	fmt.Println("✓ release-safety check: artifacts contain only project-owned output")
	return nil
}
