// Package upstream is a tiny read-only client for authoritative
// upstream release metadata (GitHub releases for aseprite/aseprite,
// aseprite/skia, Kitware/CMake and ninja-build/ninja).
//
// It performs no writes, never executes remote data, and exists so
// every subsystem derives versions and artifact URLs from upstream at
// runtime instead of embedding matrices.
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Asset is one release artifact.
type Asset struct {
	Name string
	URL  string // browser_download_url
	Size int64
	// SHA256 from GitHub's authoritative asset digest ("sha256:…"),
	// empty when upstream/GitHub does not provide one.
	SHA256 string
}

// Release is one upstream release.
type Release struct {
	Tag        string
	Draft      bool
	PreRelease bool
	Assets     []Asset
	// PublishedAt is the release publication time, when available.
	PublishedAt time.Time
}

// Asset returns the first asset matching name, or nil.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetMatching returns the first asset whose name satisfies match.
func (r Release) AssetMatching(match func(name string) bool) (Asset, bool) {
	for _, a := range r.Assets {
		if match(a.Name) {
			return a, true
		}
	}
	return Asset{}, false
}

// Client queries GitHub's public API. A token is optional; when set it
// raises rate limits for anonymous users. It is never logged.
type Client struct {
	BaseURL string // "" → https://api.github.com
	Token   string // optional; from env GITHUB_TOKEN
	HTTP    *http.Client
	// UserAgent sent on all requests.
	UserAgent string
}

// New returns a Client reading GITHUB_TOKEN from the environment.
func New() *Client {
	return &Client{
		BaseURL:   "https://api.github.com",
		Token:     os.Getenv("GITHUB_TOKEN"),
		HTTP:      &http.Client{Timeout: 2 * time.Minute},
		UserAgent: "aseprite-builder/0.1",
	}
}

// releasePayload mirrors the GitHub releases API subset.
type releasePayload struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	PreRelease bool          `json:"prerelease"`
	Published  *time.Time    `json:"published_at"`
	Assets     []assetDetail `json:"assets"`
}

type assetDetail struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

// Releases lists the latest count releases of a repo (e.g.
// "aseprite/aseprite"). Results are returned in API order (newest
// first upstream).
func (c Client) Releases(ctx context.Context, repo string, count int) ([]Release, error) {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", strings.TrimRight(c.BaseURL, "/"), repo, count)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query %s releases: %w", repo, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusForbidden:
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("query %s releases: rate limited (%s)", repo, strings.TrimSpace(string(data)))
	case resp.StatusCode != http.StatusOK:
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("query %s releases: HTTP %d (%s)", repo, resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var payloads []releasePayload
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&payloads); err != nil {
		return nil, fmt.Errorf("decode %s releases: %w", repo, err)
	}

	out := make([]Release, 0, len(payloads))
	for _, pl := range payloads {
		rel := Release{
			Tag:        pl.TagName,
			Draft:      pl.Draft,
			PreRelease: pl.PreRelease,
		}
		if pl.Published != nil {
			rel.PublishedAt = *pl.Published
		}
		rel.Assets = make([]Asset, 0, len(pl.Assets))
		for _, a := range pl.Assets {
			rel.Assets = append(rel.Assets, Asset{
				Name:   a.Name,
				URL:    a.BrowserDownloadURL,
				Size:   a.Size,
				SHA256: trimDigest(a.Digest),
			})
		}
		out = append(out, rel)
	}
	return out, nil
}

// ReleaseByTag fetches one release of repo by tag.
func (c Client) ReleaseByTag(ctx context.Context, repo, tag string) (Release, error) {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/releases/tags/%s", strings.TrimRight(c.BaseURL, "/"), repo, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return Release{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("query %s release %s: %w", repo, tag, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return Release{}, fmt.Errorf("release %s of %s not found", tag, repo)
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Release{}, fmt.Errorf("query %s release %s: HTTP %d (%s)", repo, tag, resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var pl releasePayload
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&pl); err != nil {
		return Release{}, fmt.Errorf("decode %s release %s: %w", repo, tag, err)
	}
	rel := Release{Tag: pl.TagName, Draft: pl.Draft, PreRelease: pl.PreRelease}
	if pl.Published != nil {
		rel.PublishedAt = *pl.Published
	}
	for _, a := range pl.Assets {
		rel.Assets = append(rel.Assets, Asset{
			Name:   a.Name,
			URL:    a.BrowserDownloadURL,
			Size:   a.Size,
			SHA256: trimDigest(a.Digest),
		})
	}
	return rel, nil
}

func trimDigest(digest string) string {
	if digest == "" {
		return ""
	}
	if idx := strings.Index(digest, ":"); idx >= 0 {
		return digest[idx+1:]
	}
	return digest
}
