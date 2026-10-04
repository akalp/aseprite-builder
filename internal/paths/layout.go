// Package paths manages all aseprite-builder filesystem locations.
//
// Every managed directory lives under the user's cache or application
// support directory, never next to the executable, keeping the tool
// relocatable and safe for multiple working copies.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Layout resolves, creates as needed, and reports all
// aseprite-builder-managed filesystem locations for one platform.
//
// A Layout is cheap to copy and safe for concurrent reads after New.
type Layout struct {
	// Root is the user-local cache root, e.g. ~/Library/Caches/aseprite-builder.
	Root string
	// DataRoot is the per-user data directory, e.g.
	// ~/Library/Application Support/aseprite-builder.
	DataRoot string
	// Downloads, Tools, Skia, Logs and Sources are managed
	// subdirectories of Root; State is managed under DataRoot.
	//
	// Sources holds one extracted official source tree per Aseprite
	// version; the upstream build writes into <source tree>/build, so
	// the source tree doubles as the build workspace.
	Downloads string
	Tools     string
	Skia      string
	Logs      string
	Sources   string
	State     string
}

// New builds a Layout for the current platform.
//
// Standard XDG or macOS user-local cache and data directories are
// preferred; an alternate override for the cache root exists purely for
// testing and is not part of the product surface.
func New(override string) (Layout, error) {
	cacheRoot := override
	dataRoot, err := userDataDir()
	if err != nil {
		return Layout{}, err
	}
	if override != "" {
		// Testing-only override: keep every managed path, including
		// state, inside the provided directory.
		cacheRoot = override
		dataRoot = override
	} else {
		// Documentation of os.UserCacheDir: darwin ->
		// ~/Library/Caches; XDG_CACHE_HOME, else ~/.cache on Linux;
		// %LocalAppData% on Windows.
		dir, err := os.UserCacheDir()
		if err != nil {
			return Layout{}, fmt.Errorf("resolve user cache dir: %w", err)
		}
		cacheRoot = dir
	}

	layout := Layout{
		Root:      filepath.Join(cacheRoot, "aseprite-builder"),
		DataRoot:  filepath.Join(dataRoot, "aseprite-builder"),
		Downloads: filepath.Join(cacheRoot, "aseprite-builder", "downloads"),
		Tools:     filepath.Join(cacheRoot, "aseprite-builder", "toolchains"),
		Skia:      filepath.Join(cacheRoot, "aseprite-builder", "skia"),
		Logs:      filepath.Join(cacheRoot, "aseprite-builder", "logs"),
		Sources:   filepath.Join(cacheRoot, "aseprite-builder", "sources"),
		State:     filepath.Join(dataRoot, "aseprite-builder", "state"),
	}
	return layout, nil
}

// Default returns the standard layout for the running platform.
func Default() (Layout, error) {
	return New("")
}

// EnsureDirs creates any missing directories in the layout.
func (l Layout) EnsureDirs() error {
	for _, dir := range []string{
		l.Root, l.DataRoot, l.Downloads, l.Tools, l.Skia, l.Logs, l.Sources, l.State,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// ToolDir returns the per-tool cache directory, namespaced by source
// kind and version (e.g. toolchains/cmake/official/3.31.6).
func (l Layout) ToolDir(kind, source, version string) string {
	return filepath.Join(l.Tools, strings.ToLower(kind), strings.ToLower(source), strings.ToLower(version))
}

// SourceTreeDir returns the extracted official source tree location for
// a given Aseprite version.
func (l Layout) SourceTreeDir(asepriteVersion string) string {
	return filepath.Join(l.Sources, "aseprite-"+sanitize(asepriteVersion))
}

// DownloadFile returns the target path for a downloaded artifact,
// namespaced by owning component. Both parts are reduced to single safe
// path components: remote metadata can influence asset names, and
// remote filenames are untrusted input (spec §33), so separators or
// traversal sequences can never escape the downloads directory.
func (l Layout) DownloadFile(component, name string) string {
	return filepath.Join(l.Downloads, sanitize(strings.ToLower(component)), sanitize(name))
}

func sanitize(version string) string {
	if version == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range version {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "." || out == ".." {
		return "unknown"
	}
	return out
}

// userDataDir mirrors os.UserCacheDir's semantics for application data.
func userDataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		dir := os.Getenv("LOCALAPPDATA")
		if dir == "" {
			return "", errors.New("resolve application data dir: LOCALAPPDATA is not set")
		}
		return dir, nil
	default: // unix
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			if filepath.IsAbs(xdg) {
				return xdg, nil
			}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve data home directory: %w", err)
		}
		return filepath.Join(home, ".local", "share"), nil
	}
}
