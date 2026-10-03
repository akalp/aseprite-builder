package archive

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTarGz(t *testing.T, src string, files map[string]string) {
	t.Helper()
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		mode := int64(0o644)
		if strings.HasSuffix(name, "bin/cmake") || strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if strings.HasSuffix(name, "/") {
			if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: mode}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUntarGzFlat(t *testing.T) {
	src := filepath.Join(t.TempDir(), "tool.tar.gz")
	buildTarGz(t, src, map[string]string{
		"tool-x64/bin/cmake":     "#!/bin/sh\necho cmake",
		"tool-x64/share/doc.txt": "doc",
	})
	dst := filepath.Join(t.TempDir(), "out")
	if err := UntarGz(src, dst); err != nil {
		t.Fatalf("UntarGz: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "tool-x64", "bin", "cmake")); err != nil || string(got) != "#!/bin/sh\necho cmake" {
		t.Errorf("cmake content = %q, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(dst, "tool-x64", "bin", "cmake"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("mode = %v, want executable", info.Mode())
	}
}

func TestUntarGzZipSlip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "evil.tar.gz")
	buildTarGz(t, src, map[string]string{"../evil.txt": "escaped"})
	dst := filepath.Join(t.TempDir(), "out")
	if err := UntarGz(src, dst); err == nil {
		t.Fatal("tar-slip should be rejected")
	}
}

func TestUntarGzSymlinkInside(t *testing.T) {
	src := filepath.Join(t.TempDir(), "links.tar.gz")
	buildTarGz(t, src, map[string]string{
		"tool/bin/real": "x",
	})
	// Manually add a symlink entry pointing inside the tree.
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: "tool/bin/real", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.WriteHeader(&tar.Header{Name: "tool/bin/link", Typeflag: tar.TypeSymlink, Linkname: "real"})
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = gw.Close()
	_ = f.Close()

	dst := filepath.Join(t.TempDir(), "out")
	if err := UntarGz(src, dst); err != nil {
		t.Fatalf("UntarGz: %v", err)
	}
	linkTarget, err := os.Readlink(filepath.Join(dst, "tool", "bin", "link"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if linkTarget != "real" {
		t.Errorf("link target = %q, want real", linkTarget)
	}
}

func TestUntarGzSymlinkEscape(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "escape.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: "tool/link", Typeflag: tar.TypeSymlink, Linkname: "../../../../etc/passwd"})
	_ = tw.Close()
	_ = gw.Close()
	_ = f.Close()

	dst := filepath.Join(t.TempDir(), "out")
	if err := UntarGz(filepath.Join(t.TempDir(), "escape.tar.gz"), dst); err == nil {
		t.Fatal("escaping symlink must be rejected")
	}
}

func TestTarGzTopLevelDir(t *testing.T) {
	src := filepath.Join(t.TempDir(), "wrapped.tar.gz")
	buildTarGz(t, src, map[string]string{
		"cmake-4.1.2-macos-universal/CMake.app/Contents/bin/cmake": "x",
	})
	top, wrapped, err := TarGzTopLevelDir(src)
	if err != nil {
		t.Fatalf("TarGzTopLevelDir: %v", err)
	}
	if !wrapped || top != "cmake-4.1.2-macos-universal" {
		t.Errorf("top = %q wrapped = %v", top, wrapped)
	}
}

func TestUntarGzNonGzip(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "bogus.tar.gz")
	if err := os.WriteFile(bogus, []byte("definitely not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UntarGz(bogus, filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("non-gzip input should error")
	}
}
