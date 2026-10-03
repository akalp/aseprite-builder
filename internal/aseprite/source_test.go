package aseprite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/upstream"
)

// fakeGitHub serves the upstream client's releases endpoints.
func fakeGitHub(t *testing.T, repo string, releases any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/releases") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(releases)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             any    `json:"digest"`
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	PreRelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

func srcAsset(tag string, digest any) ghAsset {
	return ghAsset{
		Name:               "Aseprite-v" + tag + "-Source.zip",
		BrowserDownloadURL: "https://example.com/Aseprite-v" + tag + "-Source.zip",
		Size:               1024,
		Digest:             digest,
	}
}

func TestResolveStableLatest(t *testing.T) {
	srv := fakeGitHub(t, "repos/aseprite/aseprite", []ghRelease{
		{TagName: "v1.3.18.6", Assets: []ghAsset{srcAsset("1.3.18.6", "sha256:fa9dd07a")}},
		{TagName: "v1.3.18-beta3", PreRelease: true, Assets: []ghAsset{srcAsset("1.3.18-beta3", nil)}},
		{TagName: "v1.3.18.1", Assets: []ghAsset{srcAsset("1.3.18.1", nil)}},
	})
	p := GithubReleaseProvider{Client: &upstream.Client{BaseURL: srv.URL}}
	rel, err := p.Resolve(context.Background(), ChannelStable)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rel.TagName != "v1.3.18.6" {
		t.Errorf("TagName = %q, want v1.3.18.6", rel.TagName)
	}
	if rel.SourceURL != "https://example.com/Aseprite-v1.3.18.6-Source.zip" {
		t.Errorf("SourceURL = %q", rel.SourceURL)
	}
	if rel.SHA256 != "fa9dd07a" {
		t.Errorf("SHA256 = %q, want fa9dd07a", rel.SHA256)
	}
	if len(rel.Version.Parts) != 4 {
		t.Errorf("version parts = %v", rel.Version.Parts)
	}
}

func TestResolveSkipsDraftAndPrerelease(t *testing.T) {
	srv := fakeGitHub(t, "repos/aseprite/aseprite", []ghRelease{
		{TagName: "v9.9.9", Draft: true, Assets: []ghAsset{srcAsset("9.9.9", nil)}},
		{TagName: "v2.0.0-rc1", PreRelease: true, Assets: []ghAsset{srcAsset("2.0.0-rc1", nil)}},
		{TagName: "v1.3.18.6", Assets: []ghAsset{srcAsset("1.3.18.6", nil)}},
	})
	p := GithubReleaseProvider{Client: &upstream.Client{BaseURL: srv.URL}}
	rel, err := p.Resolve(context.Background(), ChannelStable)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rel.TagName != "v1.3.18.6" {
		t.Errorf("TagName = %q, want v1.3.18.6", rel.TagName)
	}
}

func TestResolveSkipsSourcelessReleases(t *testing.T) {
	srv := fakeGitHub(t, "repos/aseprite/aseprite", []ghRelease{
		{TagName: "v4.0", Assets: []ghAsset{{Name: "docs.pdf"}}},
		{TagName: "v1.3.18.6", Assets: []ghAsset{{Name: "Asepite-v1.3.18.6-Source.zip", BrowserDownloadURL: "https://e/typo.zip"}}},
		{TagName: "v1.3.17", Assets: []ghAsset{srcAsset("1.3.17", nil)}},
	})
	p := GithubReleaseProvider{Client: &upstream.Client{BaseURL: srv.URL}}
	rels, err := p.List(context.Background(), ChannelStable)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rels) != 1 || rels[0].TagName != "v1.3.17" {
		t.Errorf("releases = %+v, want only v1.3.17", rels)
	}
}

func TestResolveEmpty(t *testing.T) {
	srv := fakeGitHub(t, "repos/aseprite/aseprite", []ghRelease{})
	p := GithubReleaseProvider{Client: &upstream.Client{BaseURL: srv.URL}}
	_, err := p.Resolve(context.Background(), ChannelStable)
	if err == nil || !strings.Contains(err.Error(), "no matching") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveUnsupportedChannel(t *testing.T) {
	p := GithubReleaseProvider{}
	if _, err := p.List(context.Background(), "beta"); err == nil {
		t.Error("beta channel should be unsupported")
	}
}

func TestParseChannel(t *testing.T) {
	if c, err := ParseChannel("stable"); err != nil || c != ChannelStable {
		t.Errorf("ParseChannel(stable) = %q, %v", c, err)
	}
	if c, err := ParseChannel(""); err != nil || c != ChannelStable {
		t.Errorf("ParseChannel('') = %q, %v", c, err)
	}
	if _, err := ParseChannel("beta"); err == nil {
		t.Error("ParseChannel(beta) should fail: only stable is supported")
	}
}

// TestResolveFallsBackOnRateLimit pins the resilience behavior: when
// the API is rate-limited, the latest stable release is resolved from
// the plain releases/latest redirect and the source URL is derived
// from the tag.
func TestResolveFallsBackOnRateLimit(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	t.Cleanup(api.Close)

	// releases/latest redirect: /releases/latest → 302 → /releases/tag/v1.3.18.6
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, "/aseprite/aseprite/releases/tag/v1.3.18.6", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html/>"))
	}))
	t.Cleanup(web.Close)

	var notes []string
	p := GithubReleaseProvider{
		Client:          &upstream.Client{BaseURL: api.URL},
		ReleasesBaseURL: web.URL,
		Log:             func(f string, a ...any) { notes = append(notes, fmt.Sprintf(f, a...)) },
	}
	rel, err := p.Resolve(context.Background(), ChannelStable)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rel.TagName != "v1.3.18.6" {
		t.Errorf("TagName = %q", rel.TagName)
	}
	wantURL := web.URL + "/aseprite/aseprite/releases/download/v1.3.18.6/Aseprite-v1.3.18.6-Source.zip"
	if rel.SourceURL != wantURL {
		t.Errorf("SourceURL = %q, want %q", rel.SourceURL, wantURL)
	}
	if rel.SHA256 != "" {
		t.Errorf("digest must be empty in fallback mode, got %q", rel.SHA256)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "rate-limited") {
		t.Errorf("fallback note missing: %v", notes)
	}
}

// TestResolveAPIPreferredOverFallback: a working API path must not be
// degraded to the redirect fallback.
func TestResolveAPIPreferredOverFallback(t *testing.T) {
	api := fakeGitHub(t, "repos/aseprite/aseprite", []ghRelease{
		{TagName: "v1.3.18.7", Assets: []ghAsset{srcAsset("1.3.18.7", "sha256:beef")}},
	})
	fallbackHit := false
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit = true
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(web.Close)

	p := GithubReleaseProvider{Client: &upstream.Client{BaseURL: api.URL}, ReleasesBaseURL: web.URL}
	rel, err := p.Resolve(context.Background(), ChannelStable)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rel.TagName != "v1.3.18.7" || fallbackHit {
		t.Errorf("API result not preferred: tag=%q fallback=%v", rel.TagName, fallbackHit)
	}
}

// TestResolveFallbackRejectsNonStableTag: if the redirect lands on a
// tag that is not a stable version, the builder fails instead of
// guessing.
func TestResolveFallbackRejectsNonStableTag(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(api.Close)
	// releases/latest redirect: /releases/latest → 302 → /releases/tag/v1.3.18-beta3
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, "/aseprite/aseprite/releases/tag/v1.3.18-beta3", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html/>"))
	}))
	t.Cleanup(web.Close)

	p := GithubReleaseProvider{Client: &upstream.Client{BaseURL: api.URL}, ReleasesBaseURL: web.URL}
	_, err := p.Resolve(context.Background(), ChannelStable)
	if err == nil || !strings.Contains(err.Error(), "not a recognized stable version") {
		t.Errorf("err = %v", err)
	}
}

func TestIsRateLimitError(t *testing.T) {
	if isRateLimitError(nil) {
		t.Error("nil is not a rate limit error")
	}
	if !isRateLimitError(errors.New("query releases: rate limited ({...})")) {
		t.Error("rate limited text must match")
	}
	if isRateLimitError(errors.New("connection refused")) {
		t.Error("connection errors are not rate limits")
	}
}
