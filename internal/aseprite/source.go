package aseprite

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aseprite-builder/aseprite-builder/internal/upstream"
	"github.com/aseprite-builder/aseprite-builder/internal/version"
)

// asepriteRepo is the official Aseprite GitHub repository; the only
// source of stable releases for the builder.
const asepriteRepo = "aseprite/aseprite"

// Channel identifies the distribution channel of Aseprite source.
//
// Only the stable channel is supported by the current implementation.
// Betas upstream are pre-release tags which the version parser does not
// accept as stable versions; adding more channels later means extending
// this type, not reworking the whole source-resolution layer.
type Channel string

// ChannelStable is the default and currently only supported channel.
const ChannelStable Channel = "stable"

// ParseChannel normalizes a channel string; anything except "stable"
// is unsupported and reported by the caller.
func ParseChannel(s string) (Channel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "stable", "":
		return ChannelStable, nil
	default:
		return ChannelStable, fmt.Errorf("unsupported channel %q: only %q is supported", s, ChannelStable)
	}
}

// GithubReleaseProvider implements ReleaseProvider over the public
// GitHub API without requiring Git or authentication.
type GithubReleaseProvider struct {
	// Client is the upstream metadata client. A default is used if nil.
	Client *upstream.Client
	// PageSize limits how many releases are examined (100 max).
	PageSize int
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
func (p GithubReleaseProvider) Resolve(ctx context.Context, channel Channel) (Release, error) {
	releases, err := p.List(ctx, channel)
	if err != nil {
		return Release{}, err
	}
	if len(releases) == 0 {
		return Release{}, fmt.Errorf("no matching Aseprite releases found for channel %q", channel)
	}
	return releases[0], nil
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
