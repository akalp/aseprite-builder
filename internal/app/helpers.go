package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/aseprite-builder/aseprite-builder/internal/archive"
	"github.com/aseprite-builder/aseprite-builder/internal/download"
	"github.com/aseprite-builder/aseprite-builder/internal/platform"
)

// newDownloader builds the standard verified, retrying downloader.
func (s *Service) newDownloader() *download.Downloader {
	dl := download.New()
	dl.UserAgent = "aseprite-builder/" + s.opts.BuilderVersion
	return dl
}

// extractArchive extracts an archive by content sniffing: PK zip
// signature vs 1F 8B gzip. The official source archive and Skia
// artifacts are zips; CMake toolchains are tar.gz (handled by the
// dependencies fetchers directly).
func extractArchive(pathStr, dst string) error {
	magic, err := readMagic(pathStr)
	if err != nil {
		return err
	}
	switch {
	case magic[0] == 'P' && magic[1] == 'K':
		if err := archive.Unzip(pathStr, dst); err != nil {
			return err
		}
		return flattenSingleWrapper(dst)
	case magic[0] == 0x1f && magic[1] == 0x8b:
		if err := archive.UntarGz(pathStr, dst); err != nil {
			return err
		}
		return flattenSingleWrapper(dst)
	default:
		return fmt.Errorf("unrecognized archive format in %s", pathStr)
	}
}

// readMagic reads the first bytes of a file.
func readMagic(pathStr string) ([]byte, error) {
	f, err := os.Open(pathStr) //nolint:gosec // path from trusted app logic
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 4)
	n, err := f.Read(buf)
	if n < 2 && err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:n], nil
}

// flattenSingleWrapper hoists the contents of a single wrapper
// directory up one level to normalize layouts like
// <sourceDir>/<wrapper>/CMakeLists.txt. Flat archives are unchanged.
func flattenSingleWrapper(sourceDir string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return err
	}
	var dirs []string
	var files int
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		} else {
			files++
		}
	}
	if len(dirs) != 1 || files != 0 {
		return nil // flat layout or genuinely multi-root
	}
	wrapper := filepath.Join(sourceDir, dirs[0])
	sub, err := os.ReadDir(wrapper)
	if err != nil {
		return err
	}
	for _, e := range sub {
		if err := os.Rename(filepath.Join(wrapper, e.Name()), filepath.Join(sourceDir, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(wrapper)
}

func nowUTC() time.Time { return time.Now().UTC() }

// skiaOSName maps a canonical OS name to the token upstream's
// skia-url.sh script accepts as an argument.
func skiaOSName(osName string) string {
	switch osName {
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	default:
		return ""
	}
}

// skiaCPUFor maps a Go arch string to the laf CPU token
// (platform.sh semantics: arm64 / x64 / x86).
func skiaCPUFor(arch string) string {
	switch arch {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	case "arm64":
		return "arm64"
	default:
		return ""
	}
}

// skiaPlatformFor maps a detected system into the Skia naming.
func skiaPlatformFor(info platform.SystemInfo) (string, string) {
	return skiaOSName(info.OS), skiaCPUFor(info.Arch)
}

// skiaAssetName derives the Skia asset file name from the URL the
// upstream skia-url.sh script produced (basename of the URL).
func skiaAssetName(skiaURL string) string {
	return path.Base(skiaURL)
}
