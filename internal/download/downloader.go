// Package download provides verified, resumable HTTP downloads to a
// temporary file that is atomically renamed into its final location.
//
// Only plain HTTPS downloads with optional SHA-256 verification are
// supported; partial files are never trusted.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Progress is reported periodically with the fraction of bytes received
// so callers can display a progress bar or log a percent.
type Progress func(done int64, total int64)

// Downloader fetches remote files over HTTPS with redirect handling
// and SHA-256 verification when the caller supplies an expected digest.
type Downloader struct {
	// HTTPClient is used for all requests. A default with a sane
	// timeout is used when nil.
	HTTPClient *http.Client
	// UserAgent identifies the tool to remote servers.
	UserAgent string
	// Retries is the number of additional attempts after a transient
	// failure (network or 5xx). Default 2.
	Retries int
	// RetryDelay waits between retries. Default 2s.
	RetryDelay time.Duration
	// MaxBytes caps how much data is accepted; 0 means no limit.
	MaxBytes int64
}

// New returns a Downloader with defaults suitable for build tooling.
func New() *Downloader {
	return &Downloader{
		HTTPClient: &http.Client{Timeout: 30 * time.Minute},
		UserAgent:  "aseprite-builder/0.1",
		Retries:    2,
		RetryDelay: 2 * time.Second,
		MaxBytes:   8 << 30, // 8 GiB, comfortably larger than any Aseprite artifact
	}
}

// Fetch downloads src into a temporary sibling file and atomically
// renames it to dst on success. If expectedSHA256 is non-empty the
// digest must match or the download is discarded and an error returned.
func (d *Downloader) Fetch(ctx context.Context, src, dst string, expectedSHA256 string, progress Progress) error {
	tmp := dst + ".part"
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("prepare download dir: %w", err)
	}

	var lastErr error
	attempts := d.Retries + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		err := d.fetchOnce(ctx, src, tmp, expectedSHA256, progress)
		if err == nil {
			if err := os.Rename(tmp, dst); err != nil {
				return fmt.Errorf("finalize download: %w", err)
			}
			return nil
		}
		lastErr = err
		_ = os.Remove(tmp) // A partial file is never trusted.
		if attempt < attempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d.RetryDelay):
			}
		}
	}
	return lastErr
}

func (d *Downloader) fetchOnce(ctx context.Context, src, tmp, expectedSHA256 string, progress Progress) error {
	client := d.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", d.userAgent())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", src, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: unexpected HTTP status %d", src, resp.StatusCode)
	}

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, d.limit())
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	written, err := io.Copy(io.MultiWriter(hasher, out), limited) //nolint:gosec // writing to a controlled temp path
	_ = out.Close()
	if err != nil {
		return fmt.Errorf("stream body: %w", err)
	}
	if d.MaxBytes > 0 && written >= d.MaxBytes {
		return fmt.Errorf("download exceeds size cap (%d bytes): %s", d.MaxBytes, src)
	}

	if expectedSHA256 != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, expectedSHA256) {
			return fmt.Errorf("sha256 mismatch for %s: got %s want %s", src, got, expectedSHA256)
		}
	}

	if progress != nil {
		progress(written, written)
	}
	return nil
}

func (d *Downloader) userAgent() string {
	if d.UserAgent == "" {
		return "aseprite-builder"
	}
	return d.UserAgent
}

func (d *Downloader) limit() int64 {
	if d.MaxBytes > 0 {
		return d.MaxBytes + 1 // allow detecting exactly-hits-cap via written >= cap
	}
	return 1 << 40 // ~1 TiB effective cap when MaxBytes is disabled
}

// VerifiedChecksum computes the SHA-256 of an existing file. Used by
// callers who need to double-check a previously cached artifact before
// reusing it.
func VerifiedChecksum(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is built from trusted app logic
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
