package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// UntarGz extracts a gzipped tarball into dst with the same traversal
// safety rules as Unzip.
func UntarGz(src, dst string) error {
	f, err := os.Open(src) //nolint:gosec // src comes from trusted app logic
	if err != nil {
		return fmt.Errorf("open archive %s: %w", src, err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip %s: %w", src, err)
	}
	defer func() { _ = gz.Close() }()

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create extraction dir %s: %w", dst, err)
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("resolve extraction dir: %w", err)
	}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if err := extractTarEntry(hdr, tr, dstAbs); err != nil {
			return err
		}
	}
}

func extractTarEntry(hdr *tar.Header, r io.Reader, dstAbs string) error {
	name := hdr.Name
	if name == "" {
		return nil
	}
	// Tar entry names are POSIX paths; reject absolute and
	// backslash-style forms on every platform.
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("archive entry %q uses an unsupported path form", name)
	}
	cleaned := filepath.Clean(name)
	joined := filepath.Join(dstAbs, cleaned)
	if !withinDir(dstAbs, joined) {
		return fmt.Errorf("archive entry %q escapes extraction directory", name)
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(joined, 0o755)
	case tar.TypeReg:
		return extractTarFile(hdr, r, joined)
	case tar.TypeSymlink:
		// Symlinks inside toolchain tarballs are internal (e.g. lib
		// versioning). Reject links that would point outside the tree.
		target := filepath.Clean(hdr.Linkname)
		if filepath.IsAbs(target) || strings.HasPrefix(hdr.Linkname, "/") {
			return fmt.Errorf("archive entry %q has an absolute symlink target", name)
		}
		linkDir := filepath.Dir(joined)
		resolved := filepath.Join(linkDir, target)
		if !withinDir(dstAbs, resolved) {
			return fmt.Errorf("archive entry %q symlink escapes extraction directory", name)
		}
		if err := os.MkdirAll(filepath.Dir(joined), 0o755); err != nil {
			return err
		}
		_ = os.Remove(joined)
		return os.Symlink(target, joined)
	case tar.TypeXGlobalHeader, tar.TypeXHeader:
		return nil // PAX metadata entries carry no content to extract.
	default:
		// Hardlinks, devices, fifos: not expected in toolchain
		// distributions; skip rather than fail the whole archive.
		return nil
	}
}

func extractTarFile(hdr *tar.Header, r io.Reader, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", dstPath, err)
	}
	mode := os.FileMode(hdr.Mode) & 0o7777
	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode) //nolint:gosec // dstPath was traversal-checked
	if err != nil {
		return fmt.Errorf("create %s: %w", dstPath, err)
	}
	if _, err := io.Copy(out, io.LimitReader(r, maxEntryBytes)); err != nil { //nolint:gosec // dstPath was traversal-checked
		_ = out.Close()
		return fmt.Errorf("extract %q: %w", hdr.Name, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dstPath, err)
	}
	return nil
}

// TarGzTopLevelDir reports the single top-level directory of a tar.gz
// archive, mirroring StripTopLevelDir's semantics.
func TarGzTopLevelDir(src string) (string, bool, error) {
	f, err := os.Open(src) //nolint:gosec // src comes from trusted app logic
	if err != nil {
		return "", false, fmt.Errorf("open archive %s: %w", src, err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", false, fmt.Errorf("open gzip %s: %w", src, err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	top := ""
	hasDeeper := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, fmt.Errorf("read tar entry: %w", err)
		}
		if strings.Contains(hdr.Name, "\\") {
			return "", false, errors.New("archive entry uses a backslash")
		}
		// Tar entry names are POSIX paths: use slash semantics, not the
		// host's filepath (which rewrites "/" to "\" on Windows).
		name := path.Clean(hdr.Name)
		if name == "." || name == "" {
			continue
		}
		first, _, deeper := strings.Cut(name, "/")
		if first == "" {
			return "", false, nil
		}
		if deeper {
			hasDeeper = true
		}
		if top == "" {
			top = first
		} else if top != first {
			return "", false, nil
		}
	}
	if top == "" || !hasDeeper {
		return "", false, nil
	}
	return top, true, nil
}
