package dependencies

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLinuxToolsCMakeFetchInstallsBinLayout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux distribution layouts are verified on Unix hosts (production never picks this fetcher on Windows)")
	}
	layout := testLayout(t)
	archive := mustTarGz(t, map[string]string{
		"cmake-4.4.4-linux-x86_64/bin/cmake":       "ELF",
		"cmake-4.4.4-linux-x86_64/share/cmake/doc": "doc",
	})
	client := winReleasesServer(t, "v4.4.4", "cmake-4.4.4-linux-x86_64.tar.gz", archive)

	f := NewLinuxCMakeFetcher(layout, client)
	tool, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := filepath.Join(layout.Tools, "cmake", "official", "4.4.4", "bin", "cmake")
	if tool.Path != want {
		t.Errorf("Path = %q, want %q", tool.Path, want)
	}
	if tool.BinDir != filepath.Dir(want) {
		t.Errorf("BinDir = %q", tool.BinDir)
	}
	if tool.Source != "cache" || tool.Version.String() != "4.4.4" {
		t.Errorf("tool = %+v", tool)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("bin/cmake missing: %v", err)
	}
	if _, err := os.Stat(layout.DownloadFile("cmake", "cmake-4.4.4-linux-x86_64.tar.gz")); !os.IsNotExist(err) {
		t.Error("downloaded archive should be removed")
	}
	// A second fetch reuses the cached distribution.
	tool2, err := NewLinuxCMakeFetcher(layout, client).Fetch(context.Background())
	if err != nil {
		t.Fatalf("second Fetch (cache reuse): %v", err)
	}
	if tool2.Path != want || tool2.Source != "cache" {
		t.Errorf("second tool = %+v", tool2)
	}
}

func TestLinuxToolsNinjaFetchInstallsRootBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux distribution layouts are verified on Unix hosts (production never picks this fetcher on Windows)")
	}
	layout := testLayout(t)
	archive := mustZipExec(t, map[string]string{"ninja": "ELF", "README": "n"})
	client := winReleasesServer(t, "v1.13.2", "ninja-linux.zip", archive)

	f := NewLinuxNinjaFetcher(layout, client)
	tool, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := filepath.Join(layout.Tools, "ninja", "official", "1.13.2", "ninja")
	if tool.Path != want {
		t.Errorf("Path = %q, want %q", tool.Path, want)
	}
	if tool.BinDir != layout.Tools+"/ninja/official/1.13.2" {
		t.Errorf("BinDir = %q", tool.BinDir)
	}
	if tool.Version.String() != "1.13.2" || tool.Source != "cache" {
		t.Errorf("tool = %+v", tool)
	}
	if fi, err := os.Stat(want); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("ninja binary missing or not executable: %v / %v", fi, err)
	}
}

func TestLinuxToolsFetchersRejectForeignAssets(t *testing.T) {
	layout := testLayout(t)
	// Releases carrying only macOS/Windows assets must not satisfy the
	// Linux fetchers; they exhaust the list and fail without downloading.
	client := winReleasesServer(t, "v4.4.4", "cmake-4.4.4-macos-universal.tar.gz", []byte("archive"))
	if _, err := NewLinuxCMakeFetcher(layout, client).Fetch(context.Background()); err == nil {
		t.Error("macOS-only releases must not satisfy the linux CMake fetcher")
	}
	nclient := winReleasesServer(t, "v1.13.2", "ninja-win.zip", []byte("archive"))
	if _, err := NewLinuxNinjaFetcher(layout, nclient).Fetch(context.Background()); err == nil {
		t.Error("windows-only releases must not satisfy the linux Ninja fetcher")
	}
	if _, err := os.Stat(layout.DownloadFile("cmake", "cmake-4.4.4-macos-universal.tar.gz")); !os.IsNotExist(err) {
		t.Error("linux fetcher downloaded a foreign asset")
	}
}

func TestLocateToolBinaryLinuxCMakeShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bare (extensionless) binaries are not executable on Windows")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(bin, "cmake")
	if err := os.WriteFile(want, []byte("ELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := LocateToolBinary(dir, "cmake"); got != want {
		t.Errorf("cmake locate = %q, want %q", got, want)
	}
}
