//go:build darwin

package darwin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// fixtureIconsDir creates the upstream data/icons layout with the
// size-named PNG members used by generateBundleIcons. The hd icon is
// deliberately NOT square, so the probe path is exercised.
func fixtureIconsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"ase16.png":    "x16",
		"ase32.png":    "x32",
		"ase64.png":    "x64",
		"ase128.png":   "x128",
		"ase256.png":   "x256",
		"doc16.png":    "doc16",
		"doc32.png":    "doc32",
		"doc64.png":    "doc64",
		"doc128.png":   "doc128",
		"doc256.png":   "doc256",
		"ext16.png":    "ext16",
		"ext32.png":    "ext32",
		"ext64.png":    "ext64",
		"ext128.png":   "ext128",
		"ext256.png":   "ext256",
		"hd/asehd.png": "hd-not-square-placeholder",
	}
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// iconutilFaker simulates iconutil producing the requested .icns files;
// sips probes return a non-square dimension so the hd member is skipped.
func iconutilFaker() func(runner.Command) (runner.Result, bool) {
	return func(c runner.Command) (runner.Result, bool) {
		switch {
		case c.Name == "iconutil" && len(c.Args) == 5:
			outPath := c.Args[4]
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return runner.Result{ExitCode: 1}, true
			}
			if err := os.WriteFile(outPath, []byte("icns-bytes"), 0o644); err != nil {
				return runner.Result{ExitCode: 1}, true
			}
			return runner.Result{ExitCode: 0}, true
		case c.Name == "sips":
			return runner.Result{Stdout: "pixelWidth: 512\npixelHeight: 256\n", ExitCode: 0}, true
		}
		return runner.Result{}, false
	}
}

func TestGenerateBundleIcons(t *testing.T) {
	r := &runner.FakeRunner{Match: iconutilFaker()}
	app := t.TempDir()
	iconsDir := fixtureIconsDir(t)

	if err := generateBundleIcons(context.Background(), r, app, iconsDir, nopLogger{}); err != nil {
		t.Fatalf("generateBundleIcons: %v", err)
	}
	for _, name := range []string{"Aseprite.icns", "Document.icns", "Extension.icns"} {
		if _, err := os.Stat(filepath.Join(app, "Contents", "Resources", name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	// Work dirs are cleaned up after iconutil.
	entries, _ := os.ReadDir(filepath.Join(app, "Contents", "Resources"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".iconset-") {
			t.Errorf("iconset work dir %s leaked", e.Name())
		}
	}
}

func TestGenerateBundleIconsToleratesFailure(t *testing.T) {
	// iconutil fails: the flow must continue and produce no icns, but
	// also must not error out the install.
	r := &runner.FakeRunner{Match: func(c runner.Command) (runner.Result, bool) {
		if c.Name == "iconutil" {
			return runner.Result{ExitCode: 2}, true
		}
		if c.Name == "sips" {
			return runner.Result{Stdout: "pixelWidth: 512\npixelHeight: 512\n", ExitCode: 0}, true
		}
		return runner.Result{}, false
	}}
	app := t.TempDir()
	iconsDir := fixtureIconsDir(t)
	if err := generateBundleIcons(context.Background(), r, app, iconsDir, nopLogger{}); err != nil {
		t.Fatalf("icon failure must not error the install path: %v", err)
	}
}

func TestGenerateBundleIconsNoSource(t *testing.T) {
	r := &runner.FakeRunner{}
	app := t.TempDir()
	// Empty icons dir → shortcut, no commands issued.
	if err := generateBundleIcons(context.Background(), r, app, "", nopLogger{}); err != nil {
		t.Fatalf("empty icons dir: %v", err)
	}
	if len(r.Called()) != 0 {
		t.Errorf("calls = %+v", r.Called())
	}
}

func TestParseSquareDimension(t *testing.T) {
	out := "  /path/to/png\n  pixelWidth: 1024\n  pixelHeight: 1024\n"
	if !parseSquareDimension(out, 512) {
		t.Error("1024x1024 should pass minDim 512")
	}
	if parseSquareDimension(out, 2048) {
		t.Error("1024x1024 should fail minDim 2048")
	}
	notSquare := "pixelWidth: 512\npixelHeight: 256\n"
	if parseSquareDimension(notSquare, 256) {
		t.Error("non-square must fail")
	}
	garbage := "unintelligible"
	if parseSquareDimension(garbage, 16) {
		t.Error("garbage must fail")
	}
}

func TestProbeMissingIconSkipsGracefully(t *testing.T) {
	r := &runner.FakeRunner{}
	ok, err := probeIconDimensions(context.Background(), r, filepath.Join(t.TempDir(), "absent.png"), 512)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if ok {
		t.Error("absent icon must be skipped, not an error")
	}
}
