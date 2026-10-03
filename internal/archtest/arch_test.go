// Package archtest holds cheap architecture guard tests (spec §34/§42,
// AGENTS.md dependency rules). It contains no production code.
package archtest

import (
	"go/parser"
	"go/token"
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
