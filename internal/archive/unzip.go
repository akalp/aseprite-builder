// Package archive safely extracts zip archives.
//
// All archive paths are treated as untrusted: entry names are checked
// against zip-slip traversal, absolute paths and paths escaping the
// target directory before any file is written.
package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Unzip extracts src into dst, creating dst as needed. It refuses
// archives whose entries would escape dst.
func Unzip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open archive %s: %w", src, err)
	}
	defer func() { _ = r.Close() }()

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create extraction dir %s: %w", dst, err)
	}

	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("resolve extraction dir: %w", err)
	}

	for _, f := range r.File {
		if err := extractEntry(f, dstAbs); err != nil {
			return err
		}
	}
	return nil
}

func extractEntry(f *zip.File, dstAbs string) error {
	name := f.Name
	if name == "" {
		return nil
	}

	// Zip-slip: entries that would escape the extraction root are
	// rejected regardless of platform-specific path tricks.
	cleaned := filepath.Clean(name)
	joined := filepath.Join(dstAbs, cleaned)
	if !withinDir(dstAbs, joined) {
		return fmt.Errorf("archive entry %q escapes extraction directory", name)
	}

	// Absolute entries (e.g. "/etc/passwd") become relative and then fail
	// the withinDir check only if they'd escape; treat absolute names as
	// an error explicitly to avoid platform surprises.
	if filepath.IsAbs(name) || strings.HasPrefix(name, "\\") {
		return fmt.Errorf("archive entry %q uses an absolute path", name)
	}

	// Windows-style separators inside entries are normalized by Clean on
	// POSIX, but to be safe reject names that switch separator style.
	if strings.Contains(name, "\\") {
		return fmt.Errorf("archive entry %q contains a backslash", name)
	}

	switch {
	case f.FileInfo().IsDir():
		return os.MkdirAll(joined, 0o755)
	default:
		return extractFile(f, joined)
	}
}

func extractFile(f *zip.File, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", dstPath, err)
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open archive entry %q: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()

	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
	if err != nil {
		return fmt.Errorf("create %s: %w", dstPath, err)
	}
	// Cap entry sizes well below anything real; a single entry larger
	// than 4 GiB inside the Aseprite source is a corrupted archive.
	if _, err := io.Copy(out, io.LimitReader(rc, maxEntryBytes)); err != nil { //nolint:gosec // dstPath was traversal-checked
		_ = out.Close()
		return fmt.Errorf("extract %q: %w", f.Name, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dstPath, err)
	}
	// Preserve executable bits and permissions from the zip entry.
	if err := os.Chmod(dstPath, f.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", dstPath, err)
	}
	return nil
}

// maxEntryBytes rejects absurdly large entries instead of filling the
// disk from a corrupted archive.
const maxEntryBytes = 4 << 30

// withinDir reports whether target is inside dir (equal counts as
// inside; anything escaping, including through "..", counts as outside).
func withinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// StripTopLevelDir checks whether the archive contains exactly one
// top-level directory and reports it. The official Aseprite source
// archive is known to be flat (no wrapper dir), but other artifacts
// (Skia zips) include a wrapper; extraction callers use this helper to
// normalize both shapes.
//
// A wrapper exists when every entry's first path component is the same
// name and at least one entry lives below that component.
func StripTopLevelDir(src string) (string, bool, error) {
	r, err := zip.OpenReader(src)
	if err != nil {
		return "", false, fmt.Errorf("open archive %s: %w", src, err)
	}
	defer func() { _ = r.Close() }()

	top := ""
	hasDeeper := false
	for _, f := range r.File {
		name := filepath.Clean(f.Name)
		if name == "." || name == "" {
			continue
		}
		if strings.Contains(f.Name, "\\") {
			return "", false, errors.New("archive entry uses a backslash")
		}
		first, _, deeper := strings.Cut(name, "/")
		if first == "" {
			// Entry at archive root ("foo.txt"): no wrapper possible.
			return "", false, nil
		}
		if deeper {
			hasDeeper = true
		}
		if top == "" {
			top = first
		} else if top != first {
			return "", false, nil // multiple top-level entries: no wrapper
		}
	}
	if top == "" || !hasDeeper {
		return "", false, nil
	}
	return top, true, nil
}
