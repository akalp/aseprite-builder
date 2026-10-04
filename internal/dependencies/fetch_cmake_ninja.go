package dependencies

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/akalp/aseprite-builder/internal/archive"
	"github.com/akalp/aseprite-builder/internal/download"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/upstream"
	"github.com/akalp/aseprite-builder/internal/version"
)

// cmakeRepo is CMake's authoritative release repository. Kitware
// publishes binary distributions (including macOS universal tarballs)
// as GitHub release assets.
const cmakeRepo = "Kitware/CMake"

// cmakeAssetRe matches CMake's macOS universal binary distribution
// asset, extracting the CMake version, e.g.
// cmake-4.1.2-macos-universal.tar.gz.
var cmakeAssetRe = regexp.MustCompile(`^cmake-([0-9]+\.[0-9]+\.[0-9]+)-macos-universal\.tar\.gz$`)

// windowsCmakeAssetRe matches CMake's Windows x64 binary distribution
// asset, e.g. cmake-4.1.2-windows-x86_64.zip.
var windowsCmakeAssetRe = regexp.MustCompile(`^cmake-([0-9]+\.[0-9]+\.[0-9]+)-windows-x86_64\.zip$`)

// CMakeFetcher downloads the latest stable CMake binary distribution
// into the user-local tool cache. It resolves the version at runtime
// from upstream metadata; no version is pinned.
type CMakeFetcher struct {
	Layout    paths.Layout
	Client    *upstream.Client
	HTTP      *http.Client
	UserAgent string
	// Progress reports download bytes (optional).
	Progress download.Progress
	// Log receives one line per decision; nil disables.
	Log func(format string, args ...any)

	// windows selects the Windows distribution flow
	// (cmake-*-windows-x86_64.zip → bin\cmake.exe) instead of the
	// macOS one. Set by platform-specific wiring; zero value is the
	// macOS flow so existing callers are unchanged.
	windows bool
}

// NewWindowsCMakeFetcher returns a CMakeFetcher that resolves the
// Windows x64 distribution.
func NewWindowsCMakeFetcher(layout paths.Layout, client *upstream.Client) CMakeFetcher {
	return CMakeFetcher{Layout: layout, Client: client, windows: true}
}

// Fetch implements Fetcher by resolving the newest stable CMake macOS
// universal tarball from upstream, downloading and installing it.
func (f CMakeFetcher) Fetch(ctx context.Context) (Tool, error) {
	client := f.Client
	if client == nil {
		client = upstream.New()
	}
	releases, err := client.Releases(ctx, cmakeRepo, 20)
	if err != nil {
		return Tool{}, fmt.Errorf("list CMake releases: %w", err)
	}

	re := cmakeAssetRe
	finish := f.install
	if f.windows {
		re = windowsCmakeAssetRe
		finish = f.installWindows
	}

	for _, rel := range releases {
		if rel.Draft || rel.PreRelease {
			continue
		}
		asset, ok := rel.AssetMatching(func(name string) bool { return re.MatchString(name) })
		if !ok {
			continue
		}
		m := re.FindStringSubmatch(asset.Name)
		if m == nil {
			continue
		}
		v, err := version.Parse(m[1])
		if err != nil {
			continue
		}
		return finish(ctx, asset.Name, asset.URL, asset.SHA256, v)
	}
	return Tool{}, fmt.Errorf("no CMake distribution found in the latest upstream releases for this platform")
}

func (f CMakeFetcher) install(ctx context.Context, assetName, url, sha256 string, v version.Version) (Tool, error) {
	layout := f.Layout
	cacheDir := CacheDirFor(layout, "cmake", v.String())
	if isExecutableFile(filepath.Join(cacheDir, "CMake.app", "Contents", "bin", "cmake")) {
		return f.tool(cacheDir, v) // already cached
	}

	httpClient := f.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	dl := &download.Downloader{HTTPClient: httpClient, UserAgent: f.userAgent()}
	archivePath := layout.DownloadFile("cmake", assetName)
	if f.Log != nil {
		f.Log("downloading CMake %s from %s", v, assetName)
	}
	if err := dl.Fetch(ctx, url, archivePath, sha256, f.Progress); err != nil {
		return Tool{}, fmt.Errorf("download CMake archive: %w", err)
	}
	if err := extractNormalized(archivePath, cacheDir, archive.UntarGz, archive.TarGzTopLevelDir, stripWrapperDir); err != nil {
		return Tool{}, err
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return Tool{}, fmt.Errorf("remove downloaded archive: %w", err)
	}
	t, err := f.tool(cacheDir, v)
	if err != nil {
		_ = os.RemoveAll(cacheDir)
		if f.Log != nil {
			f.Log("removing unusable CMake cache dir %s", cacheDir)
		}
		return Tool{}, fmt.Errorf("verify downloaded CMake: %w", err)
	}
	t.Source = "cache"
	return t, nil
}

func (f CMakeFetcher) tool(cacheDir string, v version.Version) (Tool, error) {
	binary := LocateToolBinary(cacheDir, "cmake")
	if binary == "" {
		return Tool{}, fmt.Errorf("CMake distribution at %s has no usable cmake binary", cacheDir)
	}
	return Tool{
		Name:    "cmake",
		Version: v,
		Path:    binary,
		BinDir:  filepath.Dir(binary),
	}, nil
}

func (f CMakeFetcher) userAgent() string {
	if f.UserAgent != "" {
		return f.UserAgent
	}
	return "aseprite-builder/0.1"
}

// installWindows installs the CMake Windows distribution: a zip whose
// cmake-<ver> wrapper directory contains bin\cmake.exe.
func (f CMakeFetcher) installWindows(ctx context.Context, assetName, url, sha256 string, v version.Version) (Tool, error) {
	layout := f.Layout
	cacheDir := CacheDirFor(layout, "cmake", v.String())
	if isExecutableFile(filepath.Join(cacheDir, "bin", "cmake.exe")) {
		t, err := f.windowsTool(cacheDir, v)
		if err == nil {
			t.Source = "cache"
		}
		return t, err // already cached
	}

	httpClient := f.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	dl := &download.Downloader{HTTPClient: httpClient, UserAgent: f.userAgent()}
	archivePath := layout.DownloadFile("cmake", assetName)
	if f.Log != nil {
		f.Log("downloading CMake %s from %s", v, assetName)
	}
	if err := dl.Fetch(ctx, url, archivePath, sha256, f.Progress); err != nil {
		return Tool{}, fmt.Errorf("download CMake archive: %w", err)
	}
	if err := extractNormalized(archivePath, cacheDir, archive.Unzip, archive.StripTopLevelDir, stripWrapperDir); err != nil {
		return Tool{}, err
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return Tool{}, fmt.Errorf("remove downloaded archive: %w", err)
	}
	t, err := f.windowsTool(cacheDir, v)
	if err != nil {
		_ = os.RemoveAll(cacheDir)
		if f.Log != nil {
			f.Log("removing unusable CMake cache dir %s", cacheDir)
		}
		return Tool{}, fmt.Errorf("verify downloaded CMake: %w", err)
	}
	t.Source = "cache"
	return t, nil
}

func (f CMakeFetcher) windowsTool(cacheDir string, v version.Version) (Tool, error) {
	binary := LocateToolBinary(cacheDir, "cmake")
	if binary == "" {
		return Tool{}, fmt.Errorf("CMake distribution at %s has no usable cmake.exe binary", cacheDir)
	}
	return Tool{
		Name:    "cmake",
		Version: v,
		Path:    binary,
		BinDir:  filepath.Dir(binary),
	}, nil
}

// ninjaRepo is Ninja's authoritative repository.
const ninjaRepo = "ninja-build/ninja"

// ninjaAssetRe matches Ninja's macOS asset (universal since 1.11).
var ninjaAssetRe = regexp.MustCompile(`^ninja-mac\.zip$`)

// windowsNinjaAssetRe matches Ninja's Windows asset.
var windowsNinjaAssetRe = regexp.MustCompile(`^ninja-win\.zip$`)

// NinjaFetcher downloads the latest stable Ninja binary into the
// user-local tool cache.
type NinjaFetcher struct {
	Layout    paths.Layout
	Client    *upstream.Client
	HTTP      *http.Client
	UserAgent string
	Progress  download.Progress
	Log       func(format string, args ...any)

	// windows selects the Windows distribution flow (ninja-win.zip →
	// ninja.exe) instead of the macOS one.
	windows bool
}

// NewWindowsNinjaFetcher returns a NinjaFetcher that resolves the
// Windows distribution.
func NewWindowsNinjaFetcher(layout paths.Layout, client *upstream.Client) NinjaFetcher {
	return NinjaFetcher{Layout: layout, Client: client, windows: true}
}

// Fetch implements Fetcher for Ninja.
func (n NinjaFetcher) Fetch(ctx context.Context) (Tool, error) {
	client := n.Client
	if client == nil {
		client = upstream.New()
	}
	releases, err := client.Releases(ctx, ninjaRepo, 20)
	if err != nil {
		return Tool{}, fmt.Errorf("list Ninja releases: %w", err)
	}

	re := ninjaAssetRe
	if n.windows {
		re = windowsNinjaAssetRe
	}

	for _, rel := range releases {
		if rel.Draft || rel.PreRelease {
			continue
		}
		asset, ok := rel.AssetMatching(func(name string) bool { return re.MatchString(name) })
		if !ok {
			continue
		}
		m := ninjaVersionTagRe.FindStringSubmatch(rel.Tag)
		if m == nil {
			continue
		}
		v, err := version.Parse(m[1])
		if err != nil {
			continue
		}
		if n.windows {
			return n.installWindows(ctx, asset.Name, asset.URL, asset.SHA256, v)
		}
		return n.install(ctx, asset.Name, asset.URL, asset.SHA256, v)
	}
	return Tool{}, fmt.Errorf("no Ninja distribution found in the latest upstream releases for this platform")
}

func (n NinjaFetcher) install(ctx context.Context, assetName, url, sha256 string, v version.Version) (Tool, error) {
	layout := n.Layout
	cacheDir := CacheDirFor(layout, "ninja", v.String())
	if isExecutableFile(filepath.Join(cacheDir, "ninja")) {
		return Tool{
			Name: "ninja", Version: v, Path: filepath.Join(cacheDir, "ninja"), BinDir: cacheDir, Source: "cache",
		}, nil
	}

	httpClient := n.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	dl := &download.Downloader{HTTPClient: httpClient, UserAgent: n.userAgent()}
	archivePath := layout.DownloadFile("ninja", assetName)
	if n.Log != nil {
		n.Log("downloading Ninja %s from %s", v, assetName)
	}
	if err := dl.Fetch(ctx, url, archivePath, sha256, n.Progress); err != nil {
		return Tool{}, fmt.Errorf("download Ninja archive: %w", err)
	}
	if err := archive.Unzip(archivePath, cacheDir); err != nil {
		return Tool{}, fmt.Errorf("extract %s: %w", assetName, err)
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return Tool{}, fmt.Errorf("remove downloaded archive: %w", err)
	}
	if !isExecutableFile(filepath.Join(cacheDir, "ninja")) {
		_ = os.RemoveAll(cacheDir)
		return Tool{}, fmt.Errorf("ninja distribution from %s extracted to %s but has no ninja binary", assetName, cacheDir)
	}
	if err := os.Chmod(filepath.Join(cacheDir, "ninja"), 0o755); err != nil {
		return Tool{}, err
	}
	return Tool{
		Name: "ninja", Version: v, Path: filepath.Join(cacheDir, "ninja"), BinDir: cacheDir, Source: "cache",
	}, nil
}

func (n NinjaFetcher) userAgent() string {
	if n.UserAgent != "" {
		return n.UserAgent
	}
	return "aseprite-builder/0.1"
}

// installWindows installs the Ninja Windows distribution: ninja-win.zip
// containing ninja.exe at the archive root.
func (n NinjaFetcher) installWindows(ctx context.Context, assetName, url, sha256 string, v version.Version) (Tool, error) {
	layout := n.Layout
	cacheDir := CacheDirFor(layout, "ninja", v.String())
	if isExecutableFile(filepath.Join(cacheDir, "ninja.exe")) {
		return n.windowsTool(cacheDir, v), nil // already cached
	}

	httpClient := n.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	dl := &download.Downloader{HTTPClient: httpClient, UserAgent: n.userAgent()}
	archivePath := layout.DownloadFile("ninja", assetName)
	if n.Log != nil {
		n.Log("downloading Ninja %s from %s", v, assetName)
	}
	if err := dl.Fetch(ctx, url, archivePath, sha256, n.Progress); err != nil {
		return Tool{}, fmt.Errorf("download Ninja archive: %w", err)
	}
	if err := archive.Unzip(archivePath, cacheDir); err != nil {
		return Tool{}, fmt.Errorf("extract %s: %w", assetName, err)
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return Tool{}, fmt.Errorf("remove downloaded archive: %w", err)
	}
	if !isExecutableFile(filepath.Join(cacheDir, "ninja.exe")) {
		_ = os.RemoveAll(cacheDir)
		return Tool{}, fmt.Errorf("ninja distribution from %s extracted to %s but has no ninja.exe binary", assetName, cacheDir)
	}
	return n.windowsTool(cacheDir, v), nil
}

func (n NinjaFetcher) windowsTool(cacheDir string, v version.Version) Tool {
	return Tool{
		Name: "ninja", Version: v, Path: filepath.Join(cacheDir, "ninja.exe"), BinDir: cacheDir, Source: "cache",
	}
}

// extractName is the extraction function signature shared by tar.gz and zip.
type extractFunc func(src, dst string) error

// topLevelFunc reports the single wrapper dir of an archive, if any.
type topLevelFunc func(src string) (string, bool, error)

// extractNormalized extracts archivePath into dst, stripping a single
// wrapper directory when the artifact has one.
func extractNormalized(archivePath, dst string, extract extractFunc, topLevel topLevelFunc, strip func(src, name string) error) error {
	if err := extract(archivePath, dst); err != nil {
		return err
	}
	name, wrapped, err := topLevel(archivePath)
	if err != nil {
		return err
	}
	if wrapped && strip != nil {
		return strip(dst, name)
	}
	return nil
}

func stripWrapperDir(dst, name string) error {
	wrapper := filepath.Join(dst, name)
	if fi, err := os.Stat(wrapper); err != nil || !fi.IsDir() {
		return fmt.Errorf("expected wrapper directory %s after extraction", wrapper)
	}
	// Move the wrapper's children up into dst, then remove the wrapper.
	entries, err := os.ReadDir(wrapper)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from := filepath.Join(wrapper, e.Name())
		to := filepath.Join(dst, e.Name())
		if _, err := os.Lstat(to); err == nil {
			return fmt.Errorf("refusing to flatten archive: %s already exists outside wrapper", to)
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return os.Remove(wrapper)
}
