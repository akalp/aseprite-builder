package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchSuccess(t *testing.T) {
	payload := []byte("hello aseprite")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "artifact.bin")
	d := New()
	if err := d.Fetch(context.Background(), srv.URL, dst, "", nil); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("content = %q want %q", got, payload)
	}
	if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
		t.Error("temp .part file should not survive success")
	}
}

func TestFetchChecksumMatch(t *testing.T) {
	payload := []byte("checksum me")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "artifact.bin")
	if err := New().Fetch(context.Background(), srv.URL, dst, hexSum, nil); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetchChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("malicious content"))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "artifact.bin")
	err := New().Fetch(context.Background(), srv.URL, dst, strings.Repeat("0", 64), nil)
	if err == nil {
		t.Fatal("checksum mismatch must fail")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("err = %v, want sha256 mismatch", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Error("failed download must not leave the final file")
	}
}

func TestFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "artifact.bin")
	d := New()
	d.Retries = 0 // no retries: fail fast
	err := d.Fetch(context.Background(), srv.URL, dst, "", nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want 404", err)
	}
}

func TestFetchRetryOn5xx(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "artifact.bin")
	d := New()
	d.RetryDelay = time.Millisecond // fast test
	if err := d.Fetch(context.Background(), srv.URL, dst, "", nil); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (two 5xx then success)", calls)
	}
}

func TestFetchMaxBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "big.bin")
	d := New()
	d.MaxBytes = 10
	err := d.Fetch(context.Background(), srv.URL, dst, "", nil)
	if err == nil || !strings.Contains(err.Error(), "size cap") {
		t.Errorf("err = %v, want size cap error", err)
	}
}

func TestFetchProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("progress"))
	}))
	defer srv.Close()

	var done, total int64
	dst := filepath.Join(t.TempDir(), "artifact.bin")
	if err := New().Fetch(context.Background(), srv.URL, dst, "", func(d, t int64) {
		done, total = d, t
	}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if done == 0 || done != total {
		t.Errorf("progress = %d/%d, want equal nonzero", done, total)
	}
}

func TestVerifiedChecksum(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f.txt")
	content := []byte("abc")
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	got, err := VerifiedChecksum(f)
	if err != nil {
		t.Fatalf("VerifiedChecksum: %v", err)
	}
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("checksum = %q", got)
	}
}

func TestVerifiedChecksumMissingFile(t *testing.T) {
	if _, err := VerifiedChecksum(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing file should error")
	}
}
