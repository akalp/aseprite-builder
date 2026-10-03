package dependencies

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aseprite-builder/aseprite-builder/internal/upstream"
)

func buildSkiaZip(t *testing.T, wrapper string, cpu string) []byte {
	t.Helper()
	// Compose a zip resembling aseprite/skia release packages.
	dirRoot := ""
	if wrapper != "" {
		dirRoot = wrapper + "/"
	}
	entries := map[string]string{
		dirRoot + "out/Release-" + cpu + "/libskia.a":               "LIBSKIA",
		dirRoot + "out/Release-" + cpu + "/libskcms.a":              "lib",
		dirRoot + "third_party/externals/libwebp/src/webp/decode.h": "webp",
	}
	return mustZip(t, entries)
}

func mustZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.zip")
	buildZipFile(t, p, files)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func ManagerTestSetup(t *testing.T) (SkiaManager, *httptest.Server, string) {
	t.Helper()
	layout := testLayout(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buildSkiaZip(t, "Skia-macOS-Release-arm64", "arm64"))
	}))
	t.Cleanup(srv.Close)
	m := SkiaManager{
		Layout:    layout,
		HTTP:      srv.Client(),
		UserAgent: "test",
	}
	return m, srv, layout.Skia
}

func TestSkiaEnsureDownloadsAndExtracts(t *testing.T) {
	m, srv, _ := ManagerTestSetup(t)
	req := SkiaRequest{
		Tag:  "m124-08a5439a6b",
		URL:  srv.URL + "/Skia-macOS-Release-arm64.zip",
		CPU:  "arm64",
		Name: "Skia-macOS-Release-arm64.zip",
	}
	res, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if res.Reused {
		t.Error("first Ensure must download")
	}
	// The managed dir must satisfy the upstream layout.
	dir := filepath.Join(m.Layout.Skia, req.Tag)
	if res.Dir != dir {
		t.Errorf("Dir = %q want %q", res.Dir, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "Release-arm64", "libskia.a")); err != nil {
		t.Errorf("libskia.a missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "third_party", "externals")); err != nil {
		t.Errorf("third_party/externals missing: %v", err)
	}
	// Metadata recorded
	meta, err := readSkiaMetadata(dir)
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Tag != req.Tag || meta.CPU != "arm64" {
		t.Errorf("metadata = %+v", meta)
	}
	// Download artifact cleaned up
	if _, err := os.Stat(m.Layout.DownloadFile("skia", req.Name)); !os.IsNotExist(err) {
		t.Error("downloaded archive should be removed after extraction")
	}
}

func TestSkiaEnsureReusesValidCache(t *testing.T) {
	m, srv, _ := ManagerTestSetup(t)
	req := SkiaRequest{
		Tag: "m124-08a5439a6b", URL: srv.URL + "/x.zip", CPU: "arm64", Name: "x.zip",
	}
	first, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.Dir != first.Dir {
		t.Errorf("second = %+v, want reuse of %q", second, first.Dir)
	}
}

func TestSkiaEnsureRedownloadsCorruptCache(t *testing.T) {
	m, srv, _ := ManagerTestSetup(t)
	req := SkiaRequest{
		Tag: "m124-08a5439a6b", URL: srv.URL + "/x.zip", CPU: "arm64", Name: "x.zip",
	}
	if _, err := m.Ensure(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// Corrupt the cache: remove libskia.a
	dir := filepath.Join(m.Layout.Skia, req.Tag)
	if err := os.Remove(filepath.Join(dir, "out", "Release-arm64", "libskia.a")); err != nil {
		t.Fatal(err)
	}
	again, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatalf("Ensure after corruption: %v", err)
	}
	if again.Reused {
		t.Error("corrupt cache must be re-downloaded")
	}
	if _, err := os.Stat(filepath.Join(again.Dir, "out", "Release-arm64", "libskia.a")); err != nil {
		t.Errorf("libskia.a restored: %v", err)
	}
}

func TestSkiaEnsureRejectsIncompleteRequest(t *testing.T) {
	m, _, _ := ManagerTestSetup(t)
	if _, err := m.Ensure(context.Background(), SkiaRequest{Tag: "t"}); err == nil {
		t.Error("incomplete request must fail")
	}
}

func TestSkiaDigestFromUpstreamRelease(t *testing.T) {
	layout := testLayout(t)
	// Fixed payload shared by the download server and the digest.
	payload := buildSkiaZip(t, "", "arm64")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "m124-08a5439a6b",
			"draft":    false,
			"assets": []map[string]any{{
				"name":                 "Skia-macOS-Release-arm64.zip",
				"browser_download_url": "https://example.com/Skia-macOS-Release-arm64.zip",
				"digest":               "sha256:" + hexSum,
			}},
		})
	}))
	t.Cleanup(api.Close)

	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(dlSrv.Close)

	m := SkiaManager{
		Layout: layout,
		Client: &upstream.Client{BaseURL: api.URL},
		HTTP:   dlSrv.Client(),
	}
	res, err := m.Ensure(context.Background(), SkiaRequest{
		Tag: "m124-08a5439a6b", URL: dlSrv.URL + "/Skia-macOS-Release-arm64.zip", CPU: "arm64", Name: "Skia-macOS-Release-arm64.zip",
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !res.Verified {
		t.Error("digest verification should succeed")
	}
	meta, _ := readSkiaMetadata(res.Dir)
	if meta.SHA256 != hexSum || !meta.Verified {
		t.Errorf("metadata = %+v", meta)
	}
}

func TestSkiaDigestMismatchFails(t *testing.T) {
	layout := testLayout(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "m124-08a5439a6b",
			"assets": []map[string]any{{
				"name":                 "Skia-macOS-Release-arm64.zip",
				"browser_download_url": "https://example.com/x.zip",
				"digest":               "sha256:" + strings.Repeat("0", 64),
			}},
		})
	}))
	t.Cleanup(api.Close)
	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buildSkiaZip(t, "", "arm64"))
	}))
	t.Cleanup(dlSrv.Close)

	m := SkiaManager{Layout: layout, Client: &upstream.Client{BaseURL: api.URL}, HTTP: dlSrv.Client()}
	_, err := m.Ensure(context.Background(), SkiaRequest{
		Tag: "m124-08a5439a6b", URL: dlSrv.URL + "/Skia-macOS-Release-arm64.zip", CPU: "arm64", Name: "Skia-macOS-Release-arm64.zip",
	})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("err = %v, want digest mismatch", err)
	}
}

func TestSkiaUnusableLayoutFails(t *testing.T) {
	layout := testLayout(t)
	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(mustZip(t, map[string]string{"not-skia.txt": "nope"}))
	}))
	t.Cleanup(dlSrv.Close)
	m := SkiaManager{Layout: layout, HTTP: dlSrv.Client()}
	_, err := m.Ensure(context.Background(), SkiaRequest{
		Tag: "tag", URL: dlSrv.URL + "/x.zip", CPU: "arm64", Name: "x.zip",
	})
	if err == nil || !strings.Contains(err.Error(), "no usable layout") {
		t.Errorf("err = %v", err)
	}
}
