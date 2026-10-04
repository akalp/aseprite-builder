// Package release builds the four spec §3 release artifacts: the
// builder executable packed per OS/arch with no other payload.
//
// It downloads and vendors nothing: the command that uses this package
// cross-compiles the local module with the Go toolchain and packs the
// result. Archive metadata is fixed, so repeated packs of the same
// binary are byte-identical.
package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// DevVersion is reported by builds that were not produced from a tag.
const DevVersion = "0.1.0-dev"

// Target describes one release artifact from spec §3.
type Target struct {
	// OS is the GOOS the binary is built for.
	OS string
	// Arch is the GOARCH the binary is built for.
	Arch string
	// Artifact is the file name created under dist/.
	Artifact string
	// Binary is the executable name inside the artifact.
	Binary string
}

// Targets returns the spec §3 artifact matrix in a stable order.
func Targets() []Target {
	return []Target{
		{OS: "darwin", Arch: "arm64", Artifact: "aseprite-builder_Darwin_arm64.tar.gz", Binary: "aseprite-builder"},
		{OS: "darwin", Arch: "amd64", Artifact: "aseprite-builder_Darwin_x86_64.tar.gz", Binary: "aseprite-builder"},
		{OS: "windows", Arch: "amd64", Artifact: "aseprite-builder_Windows_x86_64.zip", Binary: "aseprite-builder.exe"},
		{OS: "linux", Arch: "amd64", Artifact: "aseprite-builder_Linux_x86_64.tar.gz", Binary: "aseprite-builder"},
	}
}

// Version determines the version stamped into the binaries. Precedence:
// an explicit VERSION environment value, then the nearest git tag, then
// DevVersion. A leading "v" before a digit is stripped ("v1.2.0" →
// "1.2.0"), so tag names and the version command agree.
func Version(ctx context.Context, r runner.Runner, getenv func(string) string) string {
	if v := strings.TrimSpace(getenv("VERSION")); v != "" {
		return NormalizeVersion(v)
	}
	res, err := r.Run(ctx, runner.Command{Name: "git", Args: []string{"describe", "--tags", "--abbrev=0"}})
	if err != nil || !res.OK() {
		return DevVersion
	}
	v := strings.TrimSpace(res.Stdout)
	if v == "" {
		return DevVersion
	}
	return NormalizeVersion(v)
}

// NormalizeVersion strips a leading "v" when it precedes a digit.
func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && v[1] >= '0' && v[1] <= '9' {
		return v[1:]
	}
	return v
}

// Pack writes one artifact containing binary as the single entry
// t.Binary. The format follows the artifact extension (.zip for
// Windows, .tar.gz otherwise).
func Pack(out string, binary []byte, t Target) error {
	if strings.HasSuffix(t.Artifact, ".zip") {
		return writeZip(out, t.Binary, binary)
	}
	return writeTarGz(out, t.Binary, binary)
}

func writeTarGz(out, name string, binary []byte) error {
	f, err := os.Create(out) //nolint:gosec // path from trusted release logic
	if err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: name,
		Mode: 0o755,
		Size: int64(len(binary)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s header: %w", out, err)
	}
	if _, err := tw.Write(binary); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s payload: %w", out, err)
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("close %s tar: %w", out, err)
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("close %s gzip: %w", out, err)
	}
	return f.Close()
}

func writeZip(out, name string, binary []byte) error {
	f, err := os.Create(out) //nolint:gosec // path from trusted release logic
	if err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}
	zw := zip.NewWriter(f)

	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s header: %w", out, err)
	}
	if _, err := w.Write(binary); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s payload: %w", out, err)
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("close %s zip: %w", out, err)
	}
	return f.Close()
}
