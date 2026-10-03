package aseprite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aseprite-builder/aseprite-builder/internal/upstream"
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
