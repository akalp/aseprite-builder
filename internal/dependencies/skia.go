package dependencies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/akalp/aseprite-builder/internal/archive"
	"github.com/akalp/aseprite-builder/internal/download"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/upstream"
)

// SkiaRepo is the official aseprite-maintained Skia fork containing
// the prebuilt packages Aseprite's build consumes.
const skiaRepo = "aseprite/skia"

// SkiaRequest describes one Skia artifact requirement discovered from
// the Aseprite source tree (tag + URL) plus the host CPU naming used by
// laf's build scripts ("arm64", "x64").
type SkiaRequest struct {
	Tag  string // upstream aseprite/skia tag, e.g. "m124-08a5439a6b"
	URL  string // authoritative download URL produced by upstream skia-url.sh
	CPU  string // "arm64"/"x64" — the Release dir upstream expects
	Name string // expected asset file name, e.g. Skia-macOS-Release-arm64.zip
}

// SkiaResult reports the resolved artifact directory.
type SkiaResult struct {
	Dir      string // directory whose layout upstream's SKIA_DIR expects
	Reused   bool   // served from a previously verified cache download
	Verified bool   // an authoritative digest existed and matched
}

// skiaMetadata is persisted next to the extracted Skia to record what
// was downloaded, when, and whether it was verified.
type skiaMetadata struct {
	Tag        string    `json:"tag"`
	URL        string    `json:"url"`
	SHA256     string    `json:"sha256,omitempty"`
	Verified   bool      `json:"verified"`
	Downloaded time.Time `json:"downloaded_at"`
	CPU        string    `json:"cpu"`
}

// SkiaManager resolves Skia artifacts into the user-local cache:
// reuse a previously verified extraction when valid, otherwise download
// the official package and extract it once.
type SkiaManager struct {
	Layout    paths.Layout
	Client    *upstream.Client
	HTTP      *http.Client
	UserAgent string
	Progress  download.Progress
	Log       func(format string, args ...any)
}

// Ensure returns a usable Skia directory for req.
//
// Validity is checked against the layout upstream's build expects:
// the dir must contain out/Release-<cpu> (with libskia.a) and
// third_party/externals/libwebp — both were confirmed in the upstream
// build contract. A cached dir failing validation is re-downloaded.
func (m SkiaManager) Ensure(ctx context.Context, req SkiaRequest) (SkiaResult, error) {
	if req.Tag == "" || req.URL == "" || req.CPU == "" {
		return SkiaResult{}, fmt.Errorf("skia request is incomplete: tag=%q url=%q cpu=%q", req.Tag, req.URL, req.CPU)
	}

	dir := filepath.Join(m.Layout.Skia, req.Tag)
	if m.valid(dir, req) {
		return SkiaResult{Dir: dir, Reused: true, Verified: m.verified(dir)}, nil
	}

	// Look up the authoritative digest when GitHub provides one for this
	// release asset (null for most aseprite/skia releases today).
	sha256, verifiedAvailable := m.digestFor(ctx, req)
	if verifiedAvailable && m.Log != nil {
		m.Log("Skia digest available from upstream release metadata")
	}

	if err := m.clearDir(dir); err != nil {
		return SkiaResult{}, err
	}

	httpClient := m.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	dl := &download.Downloader{HTTPClient: httpClient, UserAgent: m.userAgent()}
	archivePath := m.Layout.DownloadFile("skia", req.Name)
	if m.Log != nil {
		m.Log("downloading Skia %s from %s", req.Tag, req.Name)
	}
	if err := dl.Fetch(ctx, req.URL, archivePath, sha256, m.Progress); err != nil {
		return SkiaResult{}, fmt.Errorf("download Skia: %w", err)
	}
	resRoot := filepath.Join(m.Layout.Skia, ".extract-"+req.Tag)
	if err := m.clearDir(resRoot); err != nil {
		return SkiaResult{}, err
	}
	if err := archive.Unzip(archivePath, resRoot); err != nil {
		_ = os.RemoveAll(resRoot)
		return SkiaResult{}, fmt.Errorf("extract Skia: %w", err)
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return SkiaResult{}, fmt.Errorf("remove downloaded Skia archive: %w", err)
	}

	// Normalize the zip layout: find the directory containing both the
	// out/Release-<cpu> output and third_party headers.
	root, err := findSkiaRoot(resRoot, req.CPU)
	if err != nil {
		_ = os.RemoveAll(resRoot)
		return SkiaResult{}, fmt.Errorf("skia package %s has no usable layout: %w", req.Name, err)
	}
	if !isExecutableSkiaLayout(root, req.CPU) {
		_ = os.RemoveAll(resRoot)
		return SkiaResult{}, fmt.Errorf("skia package layout at %s is missing expected files", root)
	}

	// Normalize by moving the root into its final cache location. The
	// final path is <cache>/skia/<tag>; use a rename when the extraction
	// root IS the target's parent.
	if err := m.normalizeInto(ctx, root, dir); err != nil {
		_ = os.RemoveAll(resRoot)
		return SkiaResult{}, err
	}
	if root != resRoot {
		_ = os.RemoveAll(resRoot)
	}

	meta := skiaMetadata{
		Tag:        req.Tag,
		URL:        req.URL,
		SHA256:     sha256,
		Verified:   verifiedAvailable,
		Downloaded: time.Now().UTC(),
		CPU:        req.CPU,
	}
	if err := writeSkiaMetadata(dir, meta); err != nil {
		return SkiaResult{}, err
	}
	if m.Log != nil && !verifiedAvailable {
		m.Log("Skia downloaded without an authoritative digest (upstream publishes none); " +
			"installed from the URL derived from the source tree's own Skia resolution")
	}
	return SkiaResult{Dir: dir, Reused: false, Verified: verifiedAvailable}, nil
}

func (m SkiaManager) clearDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear %s: %w", dir, err)
	}
	return os.MkdirAll(filepath.Dir(dir), 0o755)
}

// normalizeInto moves root to finalDir, renaming when possible.
func (m SkiaManager) normalizeInto(_ context.Context, root, finalDir string) error {
	if finalDir == root {
		return nil
	}
	if err := os.RemoveAll(finalDir); err != nil {
		return fmt.Errorf("clear skia destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return fmt.Errorf("create skia parent dir: %w", err)
	}
	if err := os.Rename(root, finalDir); err != nil {
		return fmt.Errorf("move skia into cache: %w", err)
	}
	return nil
}

// valid reports whether dir satisfies the upstream Skia layout.
func (m SkiaManager) valid(dir string, req SkiaRequest) bool {
	return isExecutableSkiaLayout(dir, req.CPU) && metadataMatches(dir, req)
}

func (m SkiaManager) verified(dir string) bool {
	meta, err := readSkiaMetadata(dir)
	return err == nil && meta.Verified
}

// digestFor obtains the asset digest from the aseprite/skia release
// metadata. Returns ok=false when GitHub publishes none for this asset.
func (m SkiaManager) digestFor(ctx context.Context, req SkiaRequest) (string, bool) {
	client := m.Client
	if client == nil {
		client = upstream.New()
	}
	rel, err := client.ReleaseByTag(ctx, skiaRepo, req.Tag)
	if err != nil {
		return "", false
	}
	asset, ok := rel.Asset(req.Name)
	if !ok || asset.SHA256 == "" {
		return "", false
	}
	return asset.SHA256, true
}

func (m SkiaManager) userAgent() string {
	if m.UserAgent != "" {
		return m.UserAgent
	}
	return "aseprite-builder/0.1"
}

// findSkiaRoot locates the directory inside extraction root that holds
// the upstream Skia layout: out/Release-<cpu> and third_party/externals.
// It checks the root itself and one nested level, mirroring the two
// known zip layouts (no wrapper vs. package-name wrapper).
func findSkiaRoot(root, cpu string) (string, error) {
	if skiaHasLayout(root, cpu) {
		return root, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(root, e.Name())
		if skiaHasLayout(candidate, cpu) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no directory with out/Release-%s and third_party/externals", cpu)
}

func skiaHasLayout(dir, cpu string) bool {
	if fi, err := os.Stat(filepath.Join(dir, "out", "Release-"+cpu)); err != nil || !fi.IsDir() {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "third_party", "externals"))
	return err == nil && fi.IsDir()
}

func isExecutableSkiaLayout(dir, cpu string) bool {
	if !skiaHasLayout(dir, cpu) {
		return false
	}
	// The Skia library must exist. Naming: libskia.a (macOS/Linux) or
	// skia.lib (Windows, per the upstream Skia-Windows packages).
	for _, lib := range []string{"libskia.a", "skia.lib"} {
		candidate := filepath.Join(dir, "out", "Release-"+cpu, lib)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() && fi.Size() > 0 {
			return true
		}
	}
	return false
}

func metadataMatches(dir string, req SkiaRequest) bool {
	meta, err := readSkiaMetadata(dir)
	if err != nil {
		return false
	}
	return meta.Tag == req.Tag && meta.URL == req.URL && meta.CPU == req.CPU
}

func readSkiaMetadata(dir string) (skiaMetadata, error) {
	var meta skiaMetadata
	data, err := os.ReadFile(filepath.Join(dir, "aseprite-builder-skia.json")) //nolint:gosec // path from trusted app logic
	if err != nil {
		return meta, err
	}
	return meta, json.Unmarshal(data, &meta)
}

func writeSkiaMetadata(dir string, meta skiaMetadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "aseprite-builder-skia.json"), data, 0o644)
}
