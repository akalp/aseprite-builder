package archive

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildZip writes a zip with the given entries (name → content).
func buildZip(t *testing.T, src string, files map[string]string) {
	t.Helper()
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := zip.NewWriter(f)
	for name, content := range files {
		h := &zip.FileHeader{Name: name}
		if strings.HasSuffix(name, "/") {
			h.SetMode(0o755)
		}
		fo, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if content != "" {
			if _, err := fo.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnzipFlat(t *testing.T) {
	src := filepath.Join(t.TempDir(), "flat.zip")
	buildZip(t, src, map[string]string{
		"CMakeLists.txt": "# hello",
		"src/main.go":    "package main",
	})
	dst := filepath.Join(t.TempDir(), "out")
	if err := Unzip(src, dst); err != nil {
		t.Fatalf("Unzip: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "CMakeLists.txt")); err != nil || string(got) != "# hello" {
		t.Errorf("CMakeLists.txt = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "src", "main.go")); err != nil || string(got) != "package main" {
		t.Errorf("src/main.go = %q, %v", got, err)
	}
}

func TestUnzipZipSlip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "evil.zip")
	buildZip(t, src, map[string]string{
		"../evil.txt": "escaped",
	})
	dst := filepath.Join(t.TempDir(), "out")
	err := Unzip(src, dst)
	if err == nil {
		t.Fatal("Unzip should reject zip-slip entries")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("err = %v, want escape message", err)
	}
	if _, statErr := os.Stat(filepath.Join(dst, "..", "evil.txt")); !os.IsNotExist(statErr) {
		t.Error("traversal entry must not escape the extraction dir")
	}
}

func TestUnzipRejectsAbsolutePaths(t *testing.T) {
	src := filepath.Join(t.TempDir(), "abs.zip")
	buildZip(t, src, map[string]string{"/tmp/abs.txt": "x"})
	dst := filepath.Join(t.TempDir(), "out")
	if err := Unzip(src, dst); err == nil {
		t.Fatal("absolute archive paths must be rejected")
	}
}

func TestUnzipRejectsBackslashEntries(t *testing.T) {
	src := filepath.Join(t.TempDir(), "bs.zip")
	buildZip(t, src, map[string]string{"..\\..\\evil.txt": "x"})
	dst := filepath.Join(t.TempDir(), "out")
	if err := Unzip(src, dst); err == nil {
		t.Fatal("backslash entries must be rejected")
	}
}

func TestUnzipExecBitPreserved(t *testing.T) {
	src := filepath.Join(t.TempDir(), "exec.zip")
	// Build with executable mode manually.
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "tool.sh"}
	h.SetMode(0o755)
	fo, _ := w.CreateHeader(h)
	_, _ = fo.Write([]byte("#!/bin/sh\n"))
	_ = w.Close()
	_ = f.Close()

	dst := filepath.Join(t.TempDir(), "out")
	if err := Unzip(src, dst); err != nil {
		t.Fatalf("Unzip: %v", err)
	}
	info, err := os.Stat(filepath.Join(dst, "tool.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("mode = %v, want executable bit", info.Mode())
	}
}

func TestStripTopLevelDirWrapper(t *testing.T) {
	src := filepath.Join(t.TempDir(), "wrapped.zip")
	buildZip(t, src, map[string]string{
		"Skia-macOS-Release-arm64/out/Release-arm64/libskia.a": "lib",
		"Skia-macOS-Release-arm64/include/config.h":            "h",
	})
	top, wrapped, err := StripTopLevelDir(src)
	if err != nil {
		t.Fatalf("StripTopLevelDir: %v", err)
	}
	if !wrapped || top != "Skia-macOS-Release-arm64" {
		t.Errorf("top = %q wrapped = %v, want wrapped Skia-macOS-Release-arm64", top, wrapped)
	}
}

func TestStripTopLevelDirFlat(t *testing.T) {
	src := filepath.Join(t.TempDir(), "flat.zip")
	buildZip(t, src, map[string]string{
		"CMakeLists.txt": "# x",
		"src/main.cpp":   "int main() {}",
	})
	_, wrapped, err := StripTopLevelDir(src)
	if err != nil {
		t.Fatalf("StripTopLevelDir: %v", err)
	}
	if wrapped {
		t.Error("flat archive should report no wrapper")
	}
}

func TestStripTopLevelDirMissing(t *testing.T) {
	if _, _, err := StripTopLevelDir(filepath.Join(t.TempDir(), "missing.zip")); err == nil {
		t.Error("missing archive should error")
	}
}
