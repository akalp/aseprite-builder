package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLayoutDefault(t *testing.T) {
	layout, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cacheDir, _ := os.UserCacheDir()
	dataRoot := userDataDirForTest(t)
	wantRoot := filepath.Join(cacheDir, "aseprite-builder")
	if layout.Root != wantRoot {
		t.Errorf("Root = %q, want %q", layout.Root, wantRoot)
	}
	if !strings.HasPrefix(layout.State, dataRoot) {
		t.Errorf("State %q should live under data dir %q", layout.State, dataRoot)
	}
	// Managed output must never share a single root with state.
	if layout.State == layout.Root {
		t.Error("State dir should differ from cache root")
	}
	for _, sub := range []string{layout.Downloads, layout.Tools, layout.Skia, layout.Logs, layout.Sources} {
		if !strings.HasPrefix(sub, layout.Root+string(filepath.Separator)) {
			t.Errorf("directory %q should be under cache root %q", sub, layout.Root)
		}
	}
}

func TestNewLayoutOverride(t *testing.T) {
	layout, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, dir := range []string{layout.Downloads, layout.Tools, layout.Skia, layout.Logs, layout.Sources, layout.State} {
		if !strings.HasPrefix(dir, layout.Root+string(filepath.Separator)) {
			t.Errorf("directory %q should live under override root %q", dir, layout.Root)
		}
	}
}

func TestEnsureDirs(t *testing.T) {
	layout, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, dir := range []string{layout.Root, layout.Downloads, layout.Tools, layout.Skia, layout.Logs, layout.Sources, layout.State} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("directory %s missing: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

func TestToolDir(t *testing.T) {
	layout, _ := New(t.TempDir())
	got := layout.ToolDir("CMake", "Official", "3.31.6")
	want := filepath.Join(layout.Tools, "cmake", "official", "3.31.6")
	if got != want {
		t.Errorf("ToolDir = %q, want %q", got, want)
	}
}

func TestDownloadFile(t *testing.T) {
	layout, _ := New(t.TempDir())
	got := layout.DownloadFile("Skia", "Skia-macOS-Release-arm64.zip")
	want := filepath.Join(layout.Downloads, "skia", "Skia-macOS-Release-arm64.zip")
	if got != want {
		t.Errorf("DownloadFile = %q, want %q", got, want)
	}
}

func TestDownloadFileSanitizesRemoteNames(t *testing.T) {
	layout, _ := New(t.TempDir())
	cases := map[string]string{
		"../../etc/passwd":  ".._.._etc_passwd",
		"..\\..\\evil.exe":  ".._.._evil.exe",
		"..":                "unknown",
		"/abs/path.zip":     "_abs_path.zip",
		"":                  "unknown",
		"a/b/c/../name.tgz": "a_b_c_.._name.tgz",
	}
	for in, wantName := range cases {
		got := layout.DownloadFile("aseprite", in)
		want := filepath.Join(layout.Downloads, "aseprite", wantName)
		if got != want {
			t.Errorf("DownloadFile(%q) = %q, want %q", in, got, want)
		}
		rel, err := filepath.Rel(layout.Downloads, got)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("DownloadFile(%q) escapes the downloads dir: %q", in, got)
		}
	}
}

func TestSourceTreeDir(t *testing.T) {
	layout, _ := New(t.TempDir())
	got := layout.SourceTreeDir("1.3.18.6")
	want := filepath.Join(layout.Sources, "aseprite-1.3.18.6")
	if got != want {
		t.Errorf("SourceTreeDir = %q, want %q", got, want)
	}
}

func userDataDirForTest(t *testing.T) string {
	t.Helper()
	dir, err := userDataDir()
	if err != nil {
		t.Fatalf("userDataDir: %v", err)
	}
	return dir
}
