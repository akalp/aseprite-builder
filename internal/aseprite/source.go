package aseprite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/upstream"
	"github.com/akalp/aseprite-builder/internal/version"
)

// asepriteRepo is the official Aseprite GitHub repository; the only
// source of stable releases for the builder.
const asepriteRepo = "aseprite/aseprite"

// Channel identifies the distribution channel of Aseprite source.
//
// Stable tracks the official release archives and never requires Git
// (spec §14). The development channel tracks an explicit commit of the
// official repository and requires Git.
type Channel string

// ChannelStable is the default channel: official stable releases.
const ChannelStable Channel = "stable"

// ChannelDev tracks an explicit commit of the official repository.
const ChannelDev Channel = "dev"

// ParseChannel normalizes a channel string; unknown values are
// reported by the caller.
func ParseChannel(s string) (Channel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "stable", "":
		return ChannelStable, nil
	case "dev", "development":
		return ChannelDev, nil
	default:
		return ChannelStable, fmt.Errorf("unsupported channel %q: use %q or %q", s, ChannelStable, ChannelDev)
	}
}

// GithubReleaseProvider implements ReleaseProvider over the public
// GitHub API without requiring Git or authentication.
//
// When the API's anonymous rate limit is exhausted, Resolve falls back
// to the plain (non-API) releases/latest redirect: the final URL
// carries the latest stable tag and the source archive name is derived
// from the tag exactly like the API-driven path. No digest is available
// in that mode, which is logged and reflected in the install state.
type GithubReleaseProvider struct {
	// Client is the upstream metadata client. A default is used if nil.
	Client *upstream.Client
	// PageSize limits how many releases are examined (100 max).
	PageSize int
	// Log receives decision notes (rate-limit fallback); nil disables.
	Log func(format string, args ...any)
	// ReleasesBaseURL overrides github.com for tests of the redirect fallback.
	ReleasesBaseURL string
}

// NewGithubReleaseProvider returns a provider backed by the default
// upstream client.
func NewGithubReleaseProvider() GithubReleaseProvider {
	return GithubReleaseProvider{PageSize: 100}
}

// ReleaseProvider turns a channel into a concrete Release, resolving
// data from upstream metadata rather than local assumptions.
type ReleaseProvider interface {
	Resolve(ctx context.Context, channel Channel) (Release, error)
	List(ctx context.Context, channel Channel) ([]Release, error)
}

// List enumerates Aseprite stable releases from upstream metadata.
func (p GithubReleaseProvider) List(ctx context.Context, channel Channel) ([]Release, error) {
	if channel != ChannelStable {
		return nil, fmt.Errorf("channel %q not supported; use %q", channel, ChannelStable)
	}
	client := p.Client
	if client == nil {
		client = upstream.New()
	}
	size := p.PageSize
	if size <= 0 || size > 100 {
		size = 100
	}
	raw, err := client.Releases(ctx, asepriteRepo, size)
	if err != nil {
		return nil, err
	}

	releases := make([]Release, 0, len(raw))
	for _, pl := range raw {
		rel, ok := convert(pl)
		if !ok {
			continue
		}
		releases = append(releases, rel)
	}
	sort.Slice(releases, func(i, j int) bool {
		return releases[j].Version.Less(releases[i].Version) // newest first
	})
	return releases, nil
}

// Resolve picks the latest stable release carrying a source archive.
// On API rate-limit errors it transparently falls back to the plain
// releases/latest redirect (no API quota consumed), trading off the
// asset digest, and reports the degradation through Log. Empty API
// responses do NOT degrade: they signal an upstream contract anomaly
// that must surface as an error.
func (p GithubReleaseProvider) Resolve(ctx context.Context, channel Channel) (Release, error) {
	releases, err := p.List(ctx, channel)
	if err != nil {
		if isRateLimitError(err) {
			return p.fallbackOrError(ctx, channel, err)
		}
		return Release{}, err
	}
	if len(releases) == 0 {
		return Release{}, fmt.Errorf("no matching Aseprite releases found for channel %q", channel)
	}
	return releases[0], nil
}

func (p GithubReleaseProvider) fallbackOrError(ctx context.Context, channel Channel, cause error) (Release, error) {
	if channel != ChannelStable {
		return Release{}, cause
	}
	rel, err := p.resolveViaLatestRedirect(ctx)
	if err != nil {
		return Release{}, fmt.Errorf("%w; also could not resolve the latest release via the releases/latest redirect: %w", cause, err)
	}
	if p.Log != nil {
		p.Log("GitHub API rate-limited; resolved latest stable release %s via the releases/latest redirect (no digest available for verification)", rel.TagName)
	}
	return rel, nil
}

// isRateLimitError recognizes GitHub's anonymous rate-limit responses.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "rate limit") || strings.Contains(msg, "rate-limited")
}

// latestRedirectUA identifies the builder on the plain (non-API)
// releases/latest redirect request.
const latestRedirectUA = "aseprite-builder/0.1"

// resolveViaLatestRedirect resolves the latest stable release from the
// plain releases/latest page redirect — outside the GitHub API and its
// rate limit. The final URL is
//
//	https://github.com/aseprite/aseprite/releases/tag/<tag>
//
// and the source asset name is derived from the tag by the same rule
// the API path uses (SourceAssetName). No digest is available here;
// verification degrades and is recorded accordingly.
func (p GithubReleaseProvider) resolveViaLatestRedirect(ctx context.Context) (Release, error) {
	base := p.ReleasesBaseURL
	if base == "" {
		base = "https://github.com"
	}
	url := strings.TrimRight(base, "/") + "/aseprite/aseprite/releases/latest"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return Release{}, fmt.Errorf("build latest-release request: %w", err)
	}
	req.Header.Set("User-Agent", latestRedirectUA)

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("follow latest-release redirect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a bounded body so the connection is reusable.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("latest-release redirect ended with HTTP %d", resp.StatusCode)
	}
	final := resp.Request.URL.Path
	tag := path.Base(final)
	if path.Base(path.Dir(final)) != "tag" {
		return Release{}, fmt.Errorf("latest-release redirect did not end in a tag URL (%s)", final)
	}
	v, err := ParseVersion(tag)
	if err != nil {
		// The latest stable tag is not a stable version this builder
		// understands — fail safely instead of guessing.
		return Release{}, fmt.Errorf("latest release tag %q is not a recognized stable version", tag)
	}
	return Release{
		Version: v,
		TagName: tag,
		SourceURL: fmt.Sprintf("%s/aseprite/aseprite/releases/download/%s/Aseprite-v%s-Source.zip",
			strings.TrimRight(base, "/"), tag, strings.TrimPrefix(tag, "v")),
	}, nil
}

// convert turns an upstream release into a Release, skipping releases
// that do not carry the expected source archive asset or whose tag is
// not a stable version.
func convert(pl upstream.Release) (Release, bool) {
	rel := Release{
		TagName:    pl.Tag,
		Draft:      pl.Draft,
		PreRelease: pl.PreRelease,
	}
	// Only stable releases are accepted in the stable channel.
	if !rel.Stable() {
		return Release{}, false
	}
	v, err := version.Parse(pl.Tag)
	if err != nil {
		return Release{}, false // not a stable release tag
	}
	asset, ok := pl.Asset(rel.SourceAssetName())
	if !ok {
		return Release{}, false // no usable source archive
	}
	rel.Version = v
	rel.SourceURL = asset.URL
	rel.SHA256 = asset.SHA256
	rel.AssetSize = asset.Size
	return rel, true
}
