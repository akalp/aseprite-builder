package dependencies

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/akalp/aseprite-builder/internal/upstream"
)

// winReleasesServer fakes the GitHub releases API plus the asset
// download endpoint on one server, wired to the URLs the fetchers
// produce.
func winReleasesServer(t *testing.T, tag, assetName string, archive []byte) *upstream.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			w.Header().Set("Content-Type", "application/json")
			type asset struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}
			type rel struct {
				TagName    string  `json:"tag_name"`
				Draft      bool    `json:"draft"`
				PreRelease bool    `json:"prerelease"`
				Assets     []asset `json:"assets"`
			}
			payload, _ := json.Marshal([]rel{{
				TagName: tag,
				Assets: []asset{{
					Name:               assetName,
					BrowserDownloadURL: "http://" + r.Host + "/" + assetName,
				}},
			}})
			_, _ = w.Write(payload)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	return &upstream.Client{BaseURL: srv.URL, UserAgent: "test"}
}

func TestWinToolsCMakeFetchInstallsExeLayout(t *testing.T) {
	layout := testLayout(t)
	archive := mustZip(t, map[string]string{
		"cmake-4.1.2-windows-x86_64/bin/cmake.exe":   "PE",
		"cmake-4.1.2-windows-x86_64/share/cmake/doc": "doc",
	})
	client := winReleasesServer(t, "v4.1.2", "cmake-4.1.2-windows-x86_64.zip", archive)

	f := NewWindowsCMakeFetcher(layout, client)
	tool, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := filepath.Join(layout.Tools, "cmake", "official", "4.1.2", "bin", "cmake.exe")
	if tool.Path != want {
		t.Errorf("Path = %q, want %q", tool.Path, want)
	}
	if tool.BinDir != filepath.Dir(want) {
		t.Errorf("BinDir = %q", tool.BinDir)
	}
	if tool.Source != "cache" || tool.Version.String() != "4.1.2" {
		t.Errorf("tool = %+v", tool)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("cmake.exe missing: %v", err)
	}
	if _, err := os.Stat(layout.DownloadFile("cmake", "cmake-4.1.2-windows-x86_64.zip")); !os.IsNotExist(err) {
		t.Error("downloaded archive should be removed")
	}
	// A second fetch reuses the cached distribution.
	tool2, err := NewWindowsCMakeFetcher(layout, client).Fetch(context.Background())
	if err != nil {
		t.Fatalf("second Fetch (cache reuse): %v", err)
	}
	if tool2.Path != want || tool2.Source != "cache" {
		t.Errorf("second tool = %+v", tool2)
	}
}

func TestWinToolsNinjaFetchInstallsExeLayout(t *testing.T) {
	layout := testLayout(t)
	archive := mustZip(t, map[string]string{"ninja.exe": "PE", "README": "n"})
	client := winReleasesServer(t, "v1.13.1", "ninja-win.zip", archive)

	f := NewWindowsNinjaFetcher(layout, client)
	tool, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := filepath.Join(layout.Tools, "ninja", "official", "1.13.1", "ninja.exe")
	if tool.Path != want {
		t.Errorf("Path = %q, want %q", tool.Path, want)
	}
	if tool.Version.String() != "1.13.1" || tool.Source != "cache" {
		t.Errorf("tool = %+v", tool)
	}
}

func TestWinToolsFetchersRejectForeignAssets(t *testing.T) {
	layout := testLayout(t)
	// A release list that only carries macOS assets: the windows
	// fetchers must not download them; they exhaust and fail.
	client := winReleasesServer(t, "v4.1.2", "cmake-4.1.2-macos-universal.tar.gz", []byte("archive"))
	f := NewWindowsCMakeFetcher(layout, client)
	if _, err := f.Fetch(context.Background()); err == nil {
		t.Error("macOS-only releases must not satisfy the windows CMake fetcher")
	}
	if _, err := os.Stat(layout.DownloadFile("cmake", "cmake-4.1.2-macos-universal.tar.gz")); !os.IsNotExist(err) {
		t.Error("windows fetcher downloaded the macOS asset")
	}
}

func TestWinToolsMacFetchersStillResolveMacAssets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS distribution layouts are verified on Unix hosts (production never picks this fetcher on Windows)")
	}
	layout := testLayout(t)
	archive := mustTarGz(t, map[string]string{
		"cmake-4.1.2-macos-universal/CMake.app/Contents/bin/cmake": "MACHO",
	})
	client := winReleasesServer(t, "v4.1.2", "cmake-4.1.2-macos-universal.tar.gz", archive)

	f := CMakeFetcher{Layout: layout, Client: client}
	tool, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("mac Fetch: %v", err)
	}
	if got := filepath.Base(tool.Path); got != "cmake" {
		t.Errorf("mac tool path = %q", tool.Path)
	}

	narchive := mustZipExec(t, map[string]string{"ninja": "MACHO"})
	nclient := winReleasesServer(t, "v1.13.1", "ninja-mac.zip", narchive)
	nf := NinjaFetcher{Layout: layout, Client: nclient}
	ntool, err := nf.Fetch(context.Background())
	if err != nil {
		t.Fatalf("mac ninja Fetch: %v", err)
	}
	if got := filepath.Base(ntool.Path); got != "ninja" {
		t.Errorf("mac ninja path = %q", ntool.Path)
	}
}

// mustTarGz composes a tar.gz archive (byte-equivalent to what CMake
// ships for macOS) for the mac-flow test above.
func mustTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		mode := int64(0o644)
		if strings.HasSuffix(name, "bin/cmake") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: int64(len(content)),
		}); err != nil {
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
	return buf.Bytes()
}

// mustZipExec builds a zip whose entries carry 0o755 (distributions
// like ninja-mac.zip ship executables).
func mustZipExec(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()}
		h.SetMode(0o755)
		fo, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fo.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWinToolsLocateToolBinaryWindowsShapes(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "cmake.exe"), []byte("PE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LocateToolBinary(dir, "cmake"); got != filepath.Join(bin, "cmake.exe") {
		t.Errorf("cmake locate = %q", got)
	}

	ndir := t.TempDir()
	nexe := filepath.Join(ndir, "ninja.exe")
	if err := os.WriteFile(nexe, []byte("PE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LocateToolBinary(ndir, "ninja"); got != nexe {
		t.Errorf("ninja locate = %q, want %q", got, nexe)
	}
}
