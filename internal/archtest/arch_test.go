// Package archtest holds cheap architecture guard tests (spec §34/§42,
// AGENTS.md dependency rules). It contains no production code.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uiLibraryPrefixes are the import paths considered UI dependencies.
// They are allowed only inside internal/tui (AGENTS.md).
var uiLibraryPrefixes = []string{
	"github.com/charmbracelet/",
	"github.com/muesli/",
	"github.com/mattn/go-isatty",
	"code.rocketnine.space/tslocum/cview",
	"github.com/rivo/tview",
}

// TestUILibrariesOnlyInTui fails when a Go file outside internal/tui
// imports a UI library — most importantly when internal/app gains a
// Bubble Tea import (spec §34: application services must not import
// Bubble Tea).
func TestUILibrariesOnlyInTui(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	internal := filepath.Join(root, "internal")

	fset := token.NewFileSet()
	err = filepath.Walk(internal, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "/internal/tui/") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil // test files may use UI types for plumbing asserts
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, prefix := range uiLibraryPrefixes {
				if strings.HasPrefix(p, prefix) {
					t.Errorf("%s imports UI library %q; UI dependencies are allowed only in internal/tui", path, p)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoFrontendImportsAnotherFrontend keeps internal/cli and
// internal/tui siblings: neither may import the other (both consume
// internal/app).
func TestNoFrontendImportsAnotherFrontend(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	internal := filepath.Join(root, "internal")

	fset := token.NewFileSet()
	for _, dir := range []string{"cli", "tui"} {
		dirPath := filepath.Join(internal, dir)
		other := "internal/" + map[string]string{"cli": "tui", "tui": "cli"}[dir]
		_ = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if strings.HasSuffix(p, other) {
					t.Errorf("%s must not import %s (frontends are siblings; shared code lives in internal/app)", path, p)
				}
			}
			return nil
		})
	}
}

// TestCIWorkflowCoversRequiredJobs is a lightweight presence guard for
// .github/workflows/ci.yml: it keeps the matrix from silently rotting.
// It is intentionally not a YAML parser (no new dependency for a
// workflow check) — it asserts the contract the phase promises.
func TestCIWorkflowCoversRequiredJobs(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	content := string(data)
	required := []string{
		"ubuntu-latest",
		"macos-latest",
		"windows-latest",
		// Formatting runs on the current stable Go, separately from the
		// go.mod-pinned vet/test matrix (gofmt output differs by release).
		"go-version: stable",
		"go test ./...",
		"go run ./cmd/releasecheck",
		"doctor --json",
		"make release",
	}
	for _, want := range required {
		if !strings.Contains(content, want) {
			t.Errorf("ci.yml must contain %q", want)
		}
	}
}

// TestReleaseWorkflowPublishesTaggedArtifacts keeps the tag-triggered
// release workflow aligned with the spec: build under Go, run the §38
// checker, publish through the GitHub CLI.
func TestReleaseWorkflowPublishesTaggedArtifacts(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	content := string(data)
	required := []string{
		`tags: ["v*"]`,
		"go run ./cmd/release",
		"go run ./cmd/releasecheck",
		"gh release",
		"--prerelease",
	}
	for _, want := range required {
		if !strings.Contains(content, want) {
			t.Errorf("release.yml must contain %q", want)
		}
	}
}

// TestProcessExecutionOnlyInRunner enforces the single process-execution
// path (spec §31/§33): production code outside internal/runner must not
// spawn processes directly, and no source may execute a shell string
// built from interpolated input.
func TestProcessExecutionOnlyInRunner(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	skipDirs := map[string]bool{".git": true, "dist": true, "testdata": true, ".github": true}
	forbidden := []string{`"sh", "-c"`, `"bash", "-c"`, `"powershell", "-Command"`, `"pwsh", "-Command"`}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil // tests may use os/exec for local smoke checks
		}
		if strings.Contains(filepath.ToSlash(path), "/internal/runner/") {
			return nil // the one sanctioned exec site
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		if strings.Contains(content, `"os/exec"`) {
			t.Errorf("%s imports os/exec; process execution goes through internal/runner", path)
		}
		for _, pat := range forbidden {
			if strings.Contains(content, pat) {
				t.Errorf("%s executes a shell string (%s); use direct argv execution", path, pat)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
