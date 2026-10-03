//go:build darwin

package darwin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aseprite-builder/aseprite-builder/internal/runner"
)

// iconSet describes how to build one .icns bundle icon from the
// size-named PNG members upstream ships in <source>/data/icons.
type iconSet struct {
	// Output icns file name referenced by the upstream Info.plist
	// (e.g. CFBundleIconFile → Aseprite.icns).
	Output string
	// Members map icns slot names to source PNGs (relative to iconsDir).
	Members []iconMember
	// Probed members require a sips dimension check before inclusion.
	Probed []probeMember
}

type iconMember struct {
	Slot string // icon_16x16.png, icon_32x32@2x.png …
	Src  string // e.g. ase16.png or hd/asehd.png
}

type probeMember struct {
	Slot string
	Src  string
	// MinDim: include only when the square source is at least this size.
	MinDim int
}

// The member lists mirror the upstream data/icons layout: ase*.png
// (application), doc*.png (document type icon), ext*.png (extension
// icon), plus hd/asehd.png as the large application icon. If upstream
// renames these, generation degrades to a logged warning — a bundle
// without an embedded icon is still valid.
var bundleIconSets = []iconSet{
	{
		Output: "Aseprite.icns",
		Members: []iconMember{
			{"icon_16x16.png", "ase16.png"},
			{"icon_16x16@2x.png", "ase32.png"},
			{"icon_32x32.png", "ase32.png"},
			{"icon_32x32@2x.png", "ase64.png"},
			{"icon_128x128.png", "ase128.png"},
			{"icon_128x128@2x.png", "ase256.png"},
			{"icon_256x256.png", "ase256.png"},
		},
		Probed: []probeMember{
			{Slot: "icon_512x512.png", Src: "hd/asehd.png", MinDim: 512},
			{Slot: "icon_512x512@2x.png", Src: "hd/asehd.png", MinDim: 1024},
		},
	},
	{
		Output: "Document.icns",
		Members: []iconMember{
			{"icon_16x16.png", "doc16.png"},
			{"icon_16x16@2x.png", "doc32.png"},
			{"icon_32x32.png", "doc32.png"},
			{"icon_32x32@2x.png", "doc64.png"},
			{"icon_128x128.png", "doc128.png"},
			{"icon_128x128@2x.png", "doc256.png"},
			{"icon_256x256.png", "doc256.png"},
		},
	},
	{
		Output: "Extension.icns",
		Members: []iconMember{
			{"icon_16x16.png", "ext16.png"},
			{"icon_16x16@2x.png", "ext32.png"},
			{"icon_32x32.png", "ext32.png"},
			{"icon_32x32@2x.png", "ext64.png"},
			{"icon_128x128.png", "ext128.png"},
			{"icon_128x128@2x.png", "ext256.png"},
			{"icon_256x256.png", "ext256.png"},
		},
	},
}

// warningLogger lets the icon builder log non-fatal issues.
type warningLogger interface {
	Line(format string, args ...any)
}

// generateBundleIcons builds .icns bundle icons from the upstream
// data/icons set into the staged bundle's Resources.
//
// Best-effort by design: icon generation failure never blocks an
// installation; it only affects macOS presentation. Everything runs
// unprivileged with system tools (iconutil; sips only for probing).
func generateBundleIcons(ctx context.Context, r runner.Runner, app, iconsDir string, log warningLogger) error {
	if iconsDir == "" {
		return nil
	}
	if fi, err := os.Stat(iconsDir); err != nil || !fi.IsDir() {
		return nil // no icon source: nothing to do
	}

	icnsDir := filepath.Join(app, "Contents", "Resources")
	if err := os.MkdirAll(icnsDir, 0o755); err != nil {
		return fmt.Errorf("create Resources dir: %w", err)
	}

	workRoot, err := os.MkdirTemp(icnsDir, ".iconset-")
	if err != nil {
		return fmt.Errorf("create iconset work dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workRoot) }()

	for _, set := range bundleIconSets {
		setDir := filepath.Join(workRoot, strings.TrimSuffix(set.Output, ".icns")+".iconset")
		if err := os.MkdirAll(setDir, 0o755); err != nil {
			return fmt.Errorf("create iconset dir: %w", err)
		}
		copied := 0
		for _, member := range set.Members {
			if err := copyIconMember(iconsDir, member, setDir); err != nil {
				log.Line("icon: skipping %s/%s: %v", set.Output, member.Slot, err)
				continue
			}
			copied++
		}
		for _, member := range set.Probed {
			ok, err := probeIconDimensions(ctx, r, filepath.Join(iconsDir, filepath.FromSlash(member.Src)), member.MinDim)
			if err != nil {
				log.Line("icon: probe %s failed: %v", member.Src, err)
				continue
			}
			if !ok {
				continue
			}
			if err := copyIconMember(iconsDir, iconMember{Slot: member.Slot, Src: member.Src}, setDir); err != nil {
				log.Line("icon: skipping %s/%s: %v", set.Output, member.Slot, err)
				continue
			}
			copied++
		}
		if copied == 0 {
			log.Line("icon: no members for %s in %s; skipping", set.Output, iconsDir)
			continue
		}
		out := filepath.Join(icnsDir, set.Output)
		res, err := r.Run(ctx, runner.Command{
			Name: "iconutil",
			Args: []string{"-c", "icns", setDir, "-o", out},
		})
		if err != nil {
			log.Line("icon: iconutil unavailable for %s: %v", set.Output, err)
			continue
		}
		if !res.OK() {
			log.Line("icon: iconutil failed for %s (exit %d); bundle keeps no embedded icon for it", set.Output, res.ExitCode)
			continue
		}
	}
	return nil
}

// copyIconMember copies one PNG into its icns slot name.
func copyIconMember(iconsDir string, member iconMember, setDir string) error {
	src := filepath.Join(iconsDir, filepath.FromSlash(member.Src))
	data, err := os.ReadFile(src) //nolint:gosec // paths from trusted app logic
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(setDir, member.Slot), data, 0o644)
}

// probeIconDimensions checks the source is square and large enough via
// sips (system tool). ok=false simply means skip the slot.
func probeIconDimensions(ctx context.Context, r runner.Runner, src string, minDim int) (bool, error) {
	if _, err := os.Stat(src); err != nil {
		return false, nil // absent (e.g. upstream removed hd icon): skip
	}
	res, err := r.Run(ctx, runner.Command{
		Name: "sips",
		Args: []string{"-g", "pixelWidth", "-g", "pixelHeight", src},
	})
	if err != nil {
		return false, err
	}
	if !res.OK() {
		return false, fmt.Errorf("sips exited %d", res.ExitCode)
	}
	return parseSquareDimension(res.Stdout, minDim), nil
}

// parseSquareDimension validates sips output: both dimensions equal and
// >= minDim. Extracted for tests.
func parseSquareDimension(sipsOutput string, minDim int) bool {
	width, height := -1, -1
	for _, line := range strings.Split(sipsOutput, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		switch strings.TrimSuffix(parts[0], ":") {
		case "pixelWidth":
			if n, err := parseInt(parts[1]); err == nil {
				width = n
			}
		case "pixelHeight":
			if n, err := parseInt(parts[1]); err == nil {
				height = n
			}
		}
	}
	if width < 0 || height < 0 {
		return false
	}
	return width == height && width >= minDim
}

func parseInt(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
