// Package releasecheck enforces spec §38: builder release artifacts
// must contain only project-owned output. It classifies filesystem
// trees and archive contents by name against the forbidden families
// (Aseprite binaries, Skia packages, CMake/Ninja distributions,
// Visual Studio/SDK installers, source archives, runtime caches) and
// rejects any archive entry that is not exactly the builder executable.
//
// The checker is pure and host-neutral: its tests build fake artifact
// trees and archives in temp directories, so it runs on every host.
package releasecheck

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Violation is one forbidden or unexpected artifact entry.
type Violation struct {
	// Path is the offending path, prefixed with the archive name when
	// the entry lives inside an archive.
	Path string
	// Reason names the §38 family or the allowlist rule that failed.
	Reason string
}

func (v Violation) String() string { return v.Path + ": " + v.Reason }

// allowedPayload is the complete set of entries a release artifact may
// contain: the builder executable at the archive root (spec §3).
var allowedPayload = map[string]bool{
	"aseprite-builder":     true,
	"aseprite-builder.exe": true,
}

// forbiddenPatterns maps spec §38 families to glob patterns. Patterns
// are matched case-insensitively against every path component, so
// nested cache/tool layouts are caught as well as flat names.
// Builder-owned names (aseprite-builder*) are exempted before matching.
var forbiddenPatterns = []struct {
	pattern string
	reason  string
}{
	// Aseprite binaries and bundles — the builder distributes none (§7).
	{"aseprite", "Aseprite binary"},
	{"aseprite.exe", "Aseprite binary"},
	{"aseprite.app", "Aseprite application bundle"},
	// Aseprite source archives (avoids the aseprite-builder prefix).
	{"aseprite-[0-9]*", "Aseprite source archive"},
	{"aseprite-v*", "Aseprite source archive"},
	// Skia packages.
	{"skia", "Skia package"},
	{"skia-*", "Skia package"},
	{"libskia*", "Skia package"},
	// CMake distributions.
	{"cmake-*", "CMake distribution"},
	{"cmake.app", "CMake distribution"},
	// Ninja distributions.
	{"ninja", "Ninja distribution"},
	{"ninja-*", "Ninja distribution"},
	{"ninja.exe", "Ninja distribution"},
	// Visual Studio / SDK installers.
	{"vs_*.exe", "Visual Studio installer"},
	{"vs_installer*", "Visual Studio installer"},
	{"visualstudiosetup*", "Visual Studio installer"},
	{"winsdksetup*", "Windows SDK installer"},
	{"sdksetup*", "SDK installer"},
	// Runtime caches (§15).
	{"cache", "runtime cache"},
	{"downloads", "runtime cache"},
	{"toolchains", "toolchain cache"},
	{"sources", "source cache"},
}

// CheckPaths checks each path: directories are walked, archive files
// are inspected entry-by-entry, other files are name-classified.
func CheckPaths(paths []string) ([]Violation, error) {
	var out []Violation
	for _, p := range paths {
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("release check %s: %w", p, err)
		}
		var vs []Violation
		switch {
		case info.IsDir():
			vs, err = CheckTree(p)
		case isArchiveName(p):
			vs, err = CheckArchive(p)
		default:
			if reason := forbiddenReason(filepath.Base(p)); reason != "" {
				vs = []Violation{{Path: p, Reason: reason}}
			}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, vs...)
	}
	return out, nil
}

// CheckTree walks a directory and reports any entry belonging to a
// forbidden family. It is used on dist/ before packaging and on dry-run
// artifact trees.
func CheckTree(root string) ([]Violation, error) {
	var out []Violation
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if reason := forbiddenReason(rel); reason != "" {
			out = append(out, Violation{Path: p, Reason: reason})
			if d.IsDir() {
				// One report per forbidden directory; don't repeat
				// every entry beneath it.
				return fs.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("release check %s: %w", root, err)
	}
	return out, nil
}

// CheckArchive inspects one .zip or .tar.gz artifact and reports every
// entry that is forbidden or is not the builder executable.
func CheckArchive(archivePath string) ([]Violation, error) {
	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		return checkZip(archivePath)
	}
	return checkTarGz(archivePath)
}

// isArchiveName reports whether the file name looks like a release
// archive this checker can open.
func isArchiveName(p string) bool {
	lp := strings.ToLower(p)
	return strings.HasSuffix(lp, ".zip") || strings.HasSuffix(lp, ".tar.gz") || strings.HasSuffix(lp, ".tgz")
}

func checkZip(archivePath string) ([]Violation, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("release check %s: %w", archivePath, err)
	}
	defer func() { _ = zr.Close() }()

	var out []Violation
	for _, f := range zr.File {
		name := normalizeEntry(f.Name)
		if f.FileInfo().IsDir() {
			continue
		}
		out = append(out, classifyEntry(archivePath, name, f.Mode().IsRegular())...)
	}
	return out, nil
}

func checkTarGz(archivePath string) ([]Violation, error) {
	f, err := os.Open(archivePath) //nolint:gosec // path from trusted release logic
	if err != nil {
		return nil, fmt.Errorf("release check %s: %w", archivePath, err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("release check %s: %w", archivePath, err)
	}
	tr := tar.NewReader(gz)

	var out []Violation
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("release check %s: %w", archivePath, err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue // pax metadata, not a payload entry
		}
		name := normalizeEntry(hdr.Name)
		if hdr.FileInfo().IsDir() {
			continue
		}
		out = append(out, classifyEntry(archivePath, name, hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA)...)
	}
	return out, nil
}

// classifyEntry applies the allowlist first, then the §38 families.
func classifyEntry(archivePath, name string, regular bool) []Violation {
	report := func(reason string) []Violation {
		return []Violation{{Path: archivePath + "!" + name, Reason: reason}}
	}
	if allowedPayload[strings.ToLower(name)] && regular {
		return nil
	}
	if reason := forbiddenReason(name); reason != "" {
		return report(reason)
	}
	if !regular {
		return report("non-regular entry in a release artifact")
	}
	return report("unexpected entry: release artifacts contain only the builder executable")
}

// normalizeEntry strips the ./ prefix tar tools commonly add.
func normalizeEntry(name string) string {
	name = filepath.ToSlash(name)
	return strings.TrimPrefix(name, "./")
}

// forbiddenReason reports the §38 family a path belongs to, if any.
// Every component is matched so nested cache/tool paths are caught.
func forbiddenReason(rel string) string {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." || part == ".." {
			continue
		}
		if isBuilderOwned(part) {
			continue
		}
		lower := strings.ToLower(part)
		for _, f := range forbiddenPatterns {
			if ok, _ := path.Match(f.pattern, lower); ok {
				return f.reason
			}
		}
	}
	return ""
}

// isBuilderOwned exempts the builder executable and its artifact names
// (aseprite-builder, aseprite-builder.exe, aseprite-builder_*).
func isBuilderOwned(part string) bool {
	lp := strings.ToLower(part)
	return lp == "aseprite-builder" ||
		lp == "aseprite-builder.exe" ||
		strings.HasPrefix(lp, "aseprite-builder_")
}
