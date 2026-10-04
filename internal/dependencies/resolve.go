package dependencies

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/version"
)

// Fetcher resolves the latest compatible upstream artifact for a tool
// and returns it after placing it in the cache.
type Fetcher interface {
	Fetch(ctx context.Context) (Tool, error)
}

// Resolver decides between system, cached and downloaded tools.
type Resolver struct {
	Runner runner.Runner
	Layout paths.Layout
	// Fetchers maps tool names to their upstream artifact fetchers.
	Fetchers map[string]Fetcher
	// MinVersions holds minimum constraints; the app layer fills these
	// from the Aseprite build contract before resolution.
	MinVersions map[string]version.Version
	// PreferCached checks the cached distributions before the system
	// PATH (advanced tool preference, spec §41). The default is
	// system-first (spec §4: prefer an already-installed compatible
	// system tool).
	PreferCached bool
}

// Resolve returns a usable tool of the given name.
//
//  1. The preferred reuse source: the system tool from PATH when
//     compatible (default), or the newest cached distribution when
//     PreferCached is set.
//  2. The other reuse source.
//  3. Fresh download from the authoritative upstream source.
//
// Broken candidates (present but unrunnable or unparseable) are skipped
// rather than aborting the search; if nothing works, the accumulated
// reasons are reported so the user sees *why*.
func (res *Resolver) Resolve(ctx context.Context, name string) (Tool, error) {
	min := res.MinVersions[name]
	var reasons []string

	systemCandidate := func() (Tool, bool) {
		sys, found, err := DetectSystemTool(ctx, res.Runner, name)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("system %s unusable: %v", name, err))
			return Tool{}, false
		}
		if !found {
			return Tool{}, false
		}
		if sys.Version.AtLeast(min) {
			return sys, true
		}
		reasons = append(reasons, fmt.Sprintf("system %s %s is older than required %s", name, sys.Version, min))
		return Tool{}, false
	}

	cacheCandidate := func() (Tool, bool) {
		for _, cached := range res.cachedTools(name) {
			v, err := ProbeCachedTool(ctx, res.Runner, name, cached.Path)
			if err != nil {
				reasons = append(reasons, fmt.Sprintf("cached %s at %s unusable: %v", name, cached.Path, err))
				continue // corrupted or wrong-arch entry: skip it
			}
			if v.AtLeast(min) {
				cached.Version = v
				return cached, true
			}
			reasons = append(reasons, fmt.Sprintf("cached %s %s is older than required %s", name, v, min))
		}
		return Tool{}, false
	}

	candidates := []func() (Tool, bool){systemCandidate, cacheCandidate}
	if res.PreferCached {
		candidates = []func() (Tool, bool){cacheCandidate, systemCandidate}
	}
	for _, candidate := range candidates {
		if tool, ok := candidate(); ok {
			return tool, nil
		}
	}

	// 3. Download only when neither reuse nor cache can satisfy.
	fetcher, ok := res.Fetchers[name]
	if !ok {
		return Tool{}, fmt.Errorf(
			"no compatible %s available (required: at least %s): %s",
			name, min, strings.Join(reasons, "; "))
	}
	t, err := fetcher.Fetch(ctx)
	if err != nil {
		return Tool{}, fmt.Errorf(
			"no compatible %s available (required: at least %s) and upstream download failed: %w — attempts: %s",
			name, min, err, strings.Join(reasons, "; "))
	}
	if !t.Version.AtLeast(min) {
		return Tool{}, fmt.Errorf(
			"downloaded %s %s is older than the required minimum %s", name, t.Version, min)
	}
	if v, probeErr := ProbeCachedTool(ctx, res.Runner, name, t.Path); probeErr != nil || !v.AtLeast(min) {
		res.discardCacheDir(name, t)
		if probeErr != nil {
			return Tool{}, fmt.Errorf("downloaded %s does not run on this machine: %w", name, probeErr)
		}
		return Tool{}, fmt.Errorf("downloaded %s %s is not compatible with the required minimum %s", name, v, min)
	}
	t.Source = "cache"
	return t, nil
}

// discardCacheDir removes a freshly downloaded tool distribution that
// failed validation, leaving no half-broken cache entry behind.
func (res *Resolver) discardCacheDir(name string, t Tool) {
	if t.Path == "" {
		return
	}
	// Walk up from the binary to the distribution root:
	// bin/ninja → dist dir, CMake.app/Contents/bin/cmake → dist dir.
	root := filepath.Dir(t.Path)
	if filepath.Base(root) == "bin" {
		root = filepath.Dir(root) // .../Contents/bin
		if filepath.Base(root) == "Contents" {
			root = filepath.Dir(root) // .../CMake.app
			if strings.HasSuffix(root, ".app") {
				root = filepath.Dir(root)
			}
		}
	}
	_ = os.RemoveAll(root)
}

// Check reports how a tool would be satisfied, without downloading.
// Used by doctor and the pre-install summary. The returned Check has
// Status "system", "cache", "download" or "none". The reuse ordering
// follows PreferCached so the report matches what install would do.
func (res *Resolver) Check(ctx context.Context, name string) (Check, error) {
	min := res.MinVersions[name]

	systemCheck := func() (Check, bool) {
		sys, found, err := DetectSystemTool(ctx, res.Runner, name)
		if err == nil && found && sys.Version.AtLeast(min) {
			return Check{Name: name, Status: "system", Version: sys.Version, Detail: sys.Path}, true
		}
		return Check{}, false
	}
	cacheCheck := func() (Check, bool) {
		for _, cached := range res.cachedTools(name) {
			if v, err := ProbeCachedTool(ctx, res.Runner, name, cached.Path); err == nil && v.AtLeast(min) {
				return Check{Name: name, Status: "cache", Version: v, Detail: cached.Path}, true
			}
		}
		return Check{}, false
	}

	first, second := systemCheck, cacheCheck
	if res.PreferCached {
		first, second = cacheCheck, systemCheck
	}
	if c, ok := first(); ok {
		return c, nil
	}
	if c, ok := second(); ok {
		return c, nil
	}

	if _, ok := res.Fetchers[name]; ok {
		return Check{Name: name, Status: "download", Version: min, Detail: "latest compatible from upstream"}, nil
	}
	status := "none"
	var detail string
	if sys, found, err := DetectSystemTool(ctx, res.Runner, name); err == nil && found {
		// A tool exists but cannot be verified as compatible.
		status = "incompatible"
		detail = sys.Version.String()
	}
	return Check{Name: name, Status: status, Detail: detail}, nil
}

// Check is a non-mutating resolution result for doctor/reporting.
type Check struct {
	Name    string          `json:"name"`
	Status  string          `json:"status"` // "system" | "cache" | "download" | "none"
	Version version.Version `json:"version"`
	Detail  string          `json:"detail,omitempty"`
}

// Display renders the check for human-readable output.
func (c Check) Display() string {
	parts := []string{c.Status}
	if !c.Version.IsZero() {
		parts = append(parts, c.Version.String())
	}
	if c.Detail != "" && len(c.Detail) < 80 {
		parts = append(parts, c.Detail)
	}
	return strings.Join(parts, " ")
}

// cachedTools lists cached distributions of name, newest version first.
func (res *Resolver) cachedTools(name string) []Tool {
	// Layout convention: <tools>/<kind>/<source>/<version>/
	// e.g. toolchains/cmake/official/4.1.2/
	root := filepath.Join(res.Layout.Tools, name, "official")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type entry struct {
		dir string
		v   version.Version
	}
	var found []entry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v, err := version.Parse(e.Name())
		if err != nil {
			continue
		}
		binary := LocateToolBinary(filepath.Join(root, e.Name()), name)
		if binary == "" {
			continue
		}
		found = append(found, entry{dir: filepath.Join(root, e.Name()), v: v})
	}
	sort.Slice(found, func(i, j int) bool { return found[j].v.Less(found[i].v) })
	out := make([]Tool, 0, len(found))
	for _, f := range found {
		binary := LocateToolBinary(f.dir, name)
		out = append(out, Tool{
			Name:    name,
			Version: f.v,
			Path:    binary,
			BinDir:  filepath.Dir(binary),
			Source:  "cache",
		})
	}
	return out
}

// CacheDirFor returns the cache destination directory for a fresh tool
// distribution, e.g. toolchains/cmake/official/4.1.2.
func CacheDirFor(layout paths.Layout, name, v string) string {
	return filepath.Join(layout.Tools, name, "official", strings.TrimPrefix(v, "v"))
}
