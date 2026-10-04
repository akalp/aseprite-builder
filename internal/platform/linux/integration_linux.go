//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// desktopMarker proves the builder created a desktop entry; it is
// written as a comment line and never removed from foreign files.
const desktopMarker = "# Managed by aseprite-builder."

// desktopFileName is the user-level desktop entry name (spec §22).
const desktopFileName = "aseprite.desktop"

// refreshIntegration updates the user-local CLI symlink, desktop entry
// and icon theme after a successful activation. Everything here is
// best-effort: conflicts with user-owned files are reported, never
// clobbered, and never fail an otherwise successful installation.
func (p Platform) refreshIntegration(ctx context.Context, out platform.BuildOutput) {
	if p.CLIBinDir != "" {
		if err := p.linkCLI(); err != nil {
			p.note("note: %v", err)
		}
	}
	if p.DataHome == "" {
		return
	}
	if err := p.writeDesktopEntry(out.DesktopFile); err != nil {
		p.note("note: desktop entry not installed: %v", err)
	}
	if err := p.installIcons(out.IconsDir); err != nil {
		p.note("note: icons not installed: %v", err)
	}
	// Refresh the desktop/icon caches best-effort; both tools are
	// optional and their absence is normal on minimal systems.
	p.runQuiet(ctx, "update-desktop-database", p.applicationsDir())
	p.runQuiet(ctx, "gtk-update-icon-cache", "-q", p.iconThemeDir())
}

// applicationsDir is the user-level applications directory.
func (p Platform) applicationsDir() string {
	return filepath.Join(p.DataHome, "applications")
}

// desktopEntryPath is the user-level desktop entry (spec §22).
func (p Platform) desktopEntryPath() string {
	return filepath.Join(p.applicationsDir(), desktopFileName)
}

// iconThemeDir is the user-level hicolor icon theme root.
func (p Platform) iconThemeDir() string {
	return filepath.Join(p.DataHome, "icons", "hicolor")
}

// linkCLI points ~/.local/bin/aseprite at the installed executable.
// Only a symlink inside the builder's own install root is ever
// replaced; a real file or a symlink owned elsewhere is left intact.
func (p Platform) linkCLI() error {
	if p.CLIBinDir == "" {
		return nil
	}
	if err := os.MkdirAll(p.CLIBinDir, 0o755); err != nil {
		return fmt.Errorf("create CLI bin dir: %w", err)
	}
	link := filepath.Join(p.CLIBinDir, exeName)
	desired := p.exePath()

	info, err := os.Lstat(link)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s already exists and is not a symlink; leaving it untouched", link)
		}
		existing, readErr := os.Readlink(link)
		if readErr == nil && sameLinkTarget(link, existing, desired) {
			return nil // already points at this installation
		}
		if readErr != nil || !p.ownsSymlinkTarget(link, existing) {
			return fmt.Errorf("%s points at %s (not this builder's installation); leaving it untouched", link, existing)
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("replace %s: %w", link, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// create below
	default:
		return err
	}
	if err := os.Symlink(desired, link); err != nil {
		return fmt.Errorf("create %s: %w", link, err)
	}
	return nil
}

// removeCLI removes ~/.local/bin/aseprite only when it is a symlink
// into the builder's own install root.
func (p Platform) removeCLI() {
	if p.CLIBinDir == "" {
		return
	}
	link := filepath.Join(p.CLIBinDir, exeName)
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return
	}
	target, err := os.Readlink(link)
	if err != nil || !p.ownsSymlinkTarget(link, target) {
		p.note("note: %s was not created by aseprite-builder; leaving it untouched", link)
		return
	}
	if err := os.Remove(link); err != nil {
		p.note("note: could not remove %s: %v", link, err)
	}
}

// ownsSymlinkTarget reports whether a symlink target is inside the
// builder's own install root — the only tree this tool may point at,
// replace or remove.
func (p Platform) ownsSymlinkTarget(link, target string) bool {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	return p.ownsPath(target)
}

// sameLinkTarget reports whether an existing symlink already resolves
// to the desired target (absolute or relative form).
func sameLinkTarget(link, existing, desired string) bool {
	if filepath.IsAbs(existing) {
		return filepath.Clean(existing) == filepath.Clean(desired)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(link), existing)) == filepath.Clean(desired)
}

// writeDesktopEntry writes the user-level desktop entry. When the
// extracted source ships upstream's own .desktop (src/desktop/linux/
// aseprite.desktop), it is used as the template so MIME types and
// categories track upstream without builder releases; otherwise a
// built-in minimal entry is written. A foreign file at the path is
// left untouched.
func (p Platform) writeDesktopEntry(templatePath string) error {
	if p.DataHome == "" {
		return nil
	}
	dir := p.applicationsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create applications dir: %w", err)
	}
	path := p.desktopEntryPath()
	if data, err := os.ReadFile(path); err == nil {
		if !strings.Contains(string(data), desktopMarker) {
			return fmt.Errorf("%s exists and is not managed by aseprite-builder; leaving it untouched", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte(p.renderDesktopEntry(templatePath)), 0o644)
}

// renderDesktopEntry builds the file content, preferring upstream's
// template when available.
func (p Platform) renderDesktopEntry(templatePath string) string {
	exe := p.exePath()
	if templatePath != "" {
		if data, err := os.ReadFile(templatePath); err == nil { //nolint:gosec // path from trusted app logic
			return rewriteDesktopEntry(string(data), exe)
		}
	}
	return defaultDesktopEntry(exe)
}

// rewriteDesktopEntry points an upstream .desktop entry at the
// installed executable: Exec's program token becomes the absolute path
// (field codes like %U are preserved) and TryExec becomes the absolute
// path. Every other line — MimeTypes, categories, StartupWMClass —
// stays exactly as upstream wrote it.
func rewriteDesktopEntry(content, exe string) string {
	var b strings.Builder
	b.WriteString(desktopMarker + "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "Exec="):
			rest := strings.TrimPrefix(line, "Exec=")
			_, args, _ := strings.Cut(rest, " ")
			line = "Exec=" + desktopExecQuote(exe)
			if args != "" {
				line += " " + args
			}
		case strings.HasPrefix(line, "TryExec="):
			line = "TryExec=" + exe
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// defaultDesktopEntry is the fallback used only when the source tree
// does not ship upstream's .desktop entry.
func defaultDesktopEntry(exe string) string {
	return desktopMarker + "\n" +
		"[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Aseprite\n" +
		"GenericName=Sprite Editor\n" +
		"Comment=Animated sprite editor & pixel art tool (built by aseprite-builder)\n" +
		"Icon=aseprite\n" +
		"Categories=Graphics;2DGraphics;RasterGraphics;\n" +
		"Exec=" + desktopExecQuote(exe) + " %U\n" +
		"TryExec=" + exe + "\n" +
		"Terminal=false\n" +
		"StartupNotify=false\n" +
		"StartupWMClass=Aseprite\n" +
		"MimeType=image/x-aseprite;\n"
}

// desktopExecQuote renders a path for the Exec key: quoted (with the
// reserved characters escaped per the Desktop Entry spec) when it
// contains anything outside the safe set, verbatim otherwise.
func desktopExecQuote(path string) string {
	if !strings.ContainsAny(path, " \t\n\"'\\><~|&;$*?#()`") {
		return path
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range path {
		if r == '"' || r == '\\' || r == '$' || r == '`' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// removeDesktopEntry deletes the desktop entry only when its marker
// comment proves builder ownership. Returns whether it removed the
// file (the icon cleanup gate).
func (p Platform) removeDesktopEntry() bool {
	if p.DataHome == "" {
		return false
	}
	path := p.desktopEntryPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if !strings.Contains(string(data), desktopMarker) {
		p.note("note: %s was not created by aseprite-builder; leaving it untouched", path)
		return false
	}
	if err := os.Remove(path); err != nil {
		p.note("note: could not remove %s: %v", path, err)
		return false
	}
	return true
}

// installIcons copies the upstream application icon set into the
// user's hicolor theme. Sizes are read from the PNG headers, so
// upstream changing icon sizes never needs a builder change.
func (p Platform) installIcons(iconsDir string) error {
	if iconsDir == "" || p.DataHome == "" {
		return nil
	}
	copied := 0
	// Top level: the size-named application icons (ase16.png …).
	entries, err := os.ReadDir(iconsDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !appIconNameRe.MatchString(e.Name()) {
			continue
		}
		if err := p.installIconFile(filepath.Join(iconsDir, e.Name())); err != nil {
			p.note("note: icon %s skipped: %v", e.Name(), err)
			continue
		}
		copied++
	}
	// hd/: upstream's large icon has no size in its name. The same
	// known member the macOS icon generator uses is taken, so
	// alternate artwork beside it (asehd-blush.png) is never mistaken
	// for the application icon.
	hd := filepath.Join(iconsDir, "hd", "asehd.png")
	if _, err := os.Stat(hd); err == nil {
		if err := p.installIconFile(hd); err != nil {
			p.note("note: icon hd/asehd.png skipped: %v", err)
		} else {
			copied++
		}
	}
	if copied == 0 {
		return fmt.Errorf("no usable PNG application icons in %s", iconsDir)
	}
	return nil
}

// appIconNameRe matches the upstream size-named application icons
// (ase16.png, ase256.png, …). Document/extension icons and alternate
// artwork uses other prefixes and are not the desktop entry's Icon.
var appIconNameRe = regexp.MustCompile(`^ase\d+\.png$`)

// installIconFile places one square PNG at hicolor/<w>x<w>/apps/.
func (p Platform) installIconFile(src string) error {
	width, height, err := pngSize(src)
	if err != nil {
		return err
	}
	if width != height || width < 16 {
		return fmt.Errorf("icon is %dx%d, expected a square image at least 16px", width, height)
	}
	dst := filepath.Join(p.iconThemeDir(), fmt.Sprintf("%dx%d", width, width), "apps", exeName+".png")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst, 0o644)
}

// iconSizeDirRe matches hicolor size directories (16x16, 256x256, …).
var iconSizeDirRe = regexp.MustCompile(`^\d+x\d+$`)

// removeIcons removes only the hicolor destinations this integration
// writes. It runs only after the builder-owned desktop entry was
// removed, which proves the icon entries belong to this integration;
// paths outside hicolor/<size>/apps/aseprite.png are never touched.
func (p Platform) removeIcons() {
	if p.DataHome == "" {
		return
	}
	root := p.iconThemeDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !iconSizeDirRe.MatchString(e.Name()) {
			continue
		}
		icon := filepath.Join(root, e.Name(), "apps", exeName+".png")
		if err := os.Remove(icon); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.note("note: could not remove %s: %v", icon, err)
		}
	}
}

// pngSize reads the pixel dimensions from a PNG header. Upstream's
// icon names do not always encode the size (hd/asehd.png), so the
// image itself is the source of truth.
func pngSize(path string) (int, int, error) {
	f, err := os.Open(path) //nolint:gosec // path from trusted app logic
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return 0, 0, err
	}
	if header[0] != 0x89 || string(header[1:4]) != "PNG" {
		return 0, 0, errors.New("not a PNG file")
	}
	width := int(binary.BigEndian.Uint32(header[16:20]))
	height := int(binary.BigEndian.Uint32(header[20:24]))
	return width, height, nil
}

// runQuiet runs an optional refresh tool; absence and failures are
// noted, never fatal.
func (p Platform) runQuiet(ctx context.Context, name string, args ...string) {
	res, err := p.Runner.Run(ctx, runner.Command{Name: name, Args: args})
	if err != nil {
		return // tool not installed: nothing to refresh
	}
	if !res.OK() {
		p.note("note: %s exited with code %d", name, res.ExitCode)
	}
}
