// Command releasecheck verifies that release artifacts contain only
// project-owned output (spec §38). It walks directories, inspects
// .zip/.tar.gz artifacts entry-by-entry, and exits non-zero on any
// violation.
//
// Usage: releasecheck <path>...
package main

import (
	"fmt"
	"os"

	"github.com/akalp/aseprite-builder/internal/releasecheck"
)

func main() {
	paths := os.Args[1:]
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "usage: releasecheck <path>...")
		os.Exit(2)
	}

	violations, err := releasecheck.CheckPaths(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ release check failed: %v\n", err)
		os.Exit(1)
	}
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "✗ %s\n", v)
	}
	if len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "release artifacts must contain only project-owned output (spec §38)\n")
		os.Exit(1)
	}
	fmt.Println("✓ release artifacts contain only project-owned output")
}
