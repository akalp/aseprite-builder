package releasecheck

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeTarGzFixture(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(entries) {
		body := entries[name]
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZipFixture(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, name := range sortedKeys(entries) {
		body := entries[name]
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func reasons(vs []Violation) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Reason)
	}
	return out
}

func containsReason(vs []Violation, want string) bool {
	for _, v := range vs {
		if strings.Contains(v.Reason, want) {
			return true
		}
	}
	return false
}

func TestForbiddenReason(t *testing.T) {
	cases := []struct {
		rel  string
		want string
	}{
		{"aseprite-builder", ""},
		{"aseprite-builder.exe", ""},
		{"aseprite-builder_Darwin_arm64.tar.gz", ""},
		{"aseprite-builder_Linux_x86_64.tar.gz", ""},
		{"docs/readme.md", ""},
		{"random.txt", ""},
		{"aseprite", "Aseprite binary"},
		{"bin/aseprite", "Aseprite binary"},
		{"aseprite.exe", "Aseprite binary"},
		{"Aseprite.app", "Aseprite application bundle"},
		{"Aseprite.app/Contents/MacOS/aseprite", "Aseprite application bundle"},
		{"Aseprite-v1.3.18.6-Source.zip", "Aseprite source archive"},
		{"aseprite-1.3.18.6.tar.gz", "Aseprite source archive"},
		{"Skia-m124.zip", "Skia package"},
		{"skia", "Skia package"},
		{"libskia.a", "Skia package"},
		{"cmake-4.0.0-macos-universal.tar.gz", "CMake distribution"},
		{"ninja-win.zip", "Ninja distribution"},
		{"ninja", "Ninja distribution"},
		{"vs_BuildTools.exe", "Visual Studio installer"},
		{"winsdksetup.exe", "Windows SDK installer"},
		{"cache", "runtime cache"},
		{"cache/downloads/x.part", "runtime cache"},
		{"toolchains/cmake/bin/cmake", "toolchain cache"},
		{"sources/aseprite/CMakeLists.txt", "source cache"},
	}
	for _, c := range cases {
		if got := forbiddenReason(c.rel); got != c.want {
			t.Errorf("forbiddenReason(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestCheckArchiveAcceptsTheBuilderPayload(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "aseprite-builder_Darwin_arm64.tar.gz")
	writeTarGzFixture(t, tarPath, map[string]string{"./aseprite-builder": "binary"})
	zipPath := filepath.Join(dir, "aseprite-builder_Windows_x86_64.zip")
	writeZipFixture(t, zipPath, map[string]string{"aseprite-builder.exe": "binary"})

	for _, p := range []string{tarPath, zipPath} {
		vs, err := CheckArchive(p)
		if err != nil {
			t.Fatalf("CheckArchive(%s): %v", p, err)
		}
		if len(vs) != 0 {
			t.Errorf("CheckArchive(%s) = %v, want no violations", p, vs)
		}
	}
}

func TestCheckArchiveRejectsForbiddenEntries(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "artifact.tar.gz")
	writeTarGzFixture(t, tarPath, map[string]string{
		"aseprite-builder":                     "binary",
		"Aseprite.app/Contents/MacOS/aseprite": "app",
		"Skia-m124.zip":                        "skia",
		"extra.txt":                            "unexpected",
	})

	vs, err := CheckArchive(tarPath)
	if err != nil {
		t.Fatalf("CheckArchive: %v", err)
	}
	if !containsReason(vs, "Aseprite application bundle") {
		t.Errorf("violations = %v, want the Aseprite bundle flagged", reasons(vs))
	}
	if !containsReason(vs, "Skia package") {
		t.Errorf("violations = %v, want the Skia package flagged", reasons(vs))
	}
	if !containsReason(vs, "unexpected entry") {
		t.Errorf("violations = %v, want extra.txt flagged as unexpected", reasons(vs))
	}
}

func TestCheckArchiveRejectsNestedBuilder(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "artifact.zip")
	writeZipFixture(t, zipPath, map[string]string{"sub/aseprite-builder": "binary"})

	vs, err := CheckArchive(zipPath)
	if err != nil {
		t.Fatalf("CheckArchive: %v", err)
	}
	if len(vs) != 1 || !strings.Contains(vs[0].Reason, "unexpected entry") {
		t.Errorf("violations = %v, want the nested payload rejected", vs)
	}
}

func TestCheckArchiveRejectsSourceArchiveInside(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "artifact.zip")
	writeZipFixture(t, zipPath, map[string]string{
		"aseprite-builder.exe":          "binary",
		"Aseprite-v1.3.18.6-Source.zip": "source",
	})

	vs, err := CheckArchive(zipPath)
	if err != nil {
		t.Fatalf("CheckArchive: %v", err)
	}
	if !containsReason(vs, "Aseprite source archive") {
		t.Errorf("violations = %v, want the source archive flagged", reasons(vs))
	}
}

func TestCheckTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cache", "downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"aseprite-builder_Darwin_arm64.tar.gz",
		"aseprite-builder_Linux_x86_64.tar.gz",
		"Skia-m124.zip",
		"cmake-4.0.0-linux-x86_64.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "cache", "downloads", "x.part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	vs, err := CheckTree(dir)
	if err != nil {
		t.Fatalf("CheckTree: %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("violations = %v, want 3", vs)
	}
	if !containsReason(vs, "Skia package") {
		t.Errorf("violations = %v, want the Skia package flagged", reasons(vs))
	}
	if !containsReason(vs, "CMake distribution") {
		t.Errorf("violations = %v, want the CMake distribution flagged", reasons(vs))
	}
	if !containsReason(vs, "runtime cache") {
		t.Errorf("violations = %v, want the cache directory flagged once", reasons(vs))
	}
}

func TestCheckPathsRoutesTreesAndArchives(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "ninja-win.zip"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "payload.tar.gz")
	writeTarGzFixture(t, archive, map[string]string{"aseprite-builder": "binary"})
	loose := filepath.Join(dir, "Aseprite.exe")
	if err := os.WriteFile(loose, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	vs, err := CheckPaths([]string{tree, archive, loose})
	if err != nil {
		t.Fatalf("CheckPaths: %v", err)
	}
	if !containsReason(vs, "Ninja distribution") {
		t.Errorf("violations = %v, want the tree's ninja flagged", reasons(vs))
	}
	if !containsReason(vs, "Aseprite binary") {
		t.Errorf("violations = %v, want the loose Aseprite.exe flagged", reasons(vs))
	}
	for _, v := range vs {
		if strings.Contains(v.Path, "payload.tar.gz") {
			t.Errorf("violations = %v, the clean archive must pass", vs)
		}
	}
}
