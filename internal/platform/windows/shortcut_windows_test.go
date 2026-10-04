//go:build windows

package windows

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// utf16 decode helper for assertions.
func utf16From(b []byte) string {
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return syscall.UTF16ToString(units)
}

func TestWriteShellLinkByteLayout(t *testing.T) {
	dir := t.TempDir()
	lnk := filepath.Join(dir, "Aseprite.lnk")
	exe := `C:\Users\üser\AppData\Local\Programs\Aseprite\aseprite.exe`
	workdir := `C:\Users\üser\AppData\Local\Programs\Aseprite`
	target := ShortcutTarget{
		ExePath:     exe,
		WorkingDir:  workdir,
		IconPath:    exe,
		Description: "Aseprite (installed by aseprite-builder)",
	}
	if err := WriteShellLink(lnk, target); err != nil {
		t.Fatalf("WriteShellLink: %v", err)
	}
	data, err := os.ReadFile(lnk)
	if err != nil {
		t.Fatal(err)
	}

	// --- ShellLinkHeader (§2.1) -------------------------------------
	if got := binary.LittleEndian.Uint32(data[0:4]); got != shellLinkHeaderSize {
		t.Errorf("HeaderSize = 0x%X, want 0x4C", got)
	}
	wantGUID := []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}
	if !bytes.Equal(data[4:20], wantGUID) {
		t.Errorf("LinkCLSID = % X", data[4:20])
	}
	flags := binary.LittleEndian.Uint32(data[20:24])
	for name, bit := range map[string]uint32{
		"HasLinkInfo":     linkFlagHasLinkInfo,
		"HasName":         linkFlagHasName,
		"HasWorkingDir":   linkFlagHasWorkingDir,
		"HasIconLocation": linkFlagHasIconLoc,
		"IsUnicode":       linkFlagIsUnicode,
	} {
		if flags&bit == 0 {
			t.Errorf("LinkFlags missing %s (0x%X)", name, flags)
		}
	}
	if attr := binary.LittleEndian.Uint32(data[24:28]); attr != fileAttrNormal {
		t.Errorf("FileAttributes = 0x%X", attr)
	}
	if sc := binary.LittleEndian.Uint32(data[60:64]); sc != showCommandNormal {
		t.Errorf("ShowCommand = 0x%X", sc)
	}

	// --- LinkInfo (§2.3, header 0x24 with Unicode offsets) ----------
	off := int(shellLinkHeaderSize)
	liSize := int(binary.LittleEndian.Uint32(data[off:]))
	liHeader := int(binary.LittleEndian.Uint32(data[off+4:]))
	liFlags := binary.LittleEndian.Uint32(data[off+8:])
	if liHeader != linkInfoHeaderSizeUnicode {
		t.Errorf("LinkInfoHeaderSize = 0x%X, want 0x24", liHeader)
	}
	if liFlags&linkInfoVolumeAndBasePath == 0 {
		t.Errorf("LinkInfoFlags = 0x%X, want VolumeIDAndLocalBasePath", liFlags)
	}
	localBaseOffA := int(binary.LittleEndian.Uint32(data[off+16:]))
	suffixOffA := int(binary.LittleEndian.Uint32(data[off+24:]))
	localBaseOffU := int(binary.LittleEndian.Uint32(data[off+28:]))
	suffixOffU := int(binary.LittleEndian.Uint32(data[off+32:]))

	// ANSI NUL-terminated base path.
	ansiBase := data[off+localBaseOffA:]
	if i := bytes.IndexByte(ansiBase, 0); i >= 0 {
		ansiBase = ansiBase[:i]
	}
	if got := string(ansiBase); !strings.HasPrefix(got, `C:\Users\?ser`) {
		t.Errorf("ANSI LocalBasePath = %q (lossy; non-ASCII must degrade, not break)", got)
	}
	// Unicode base path must be exact.
	uBase := data[off+localBaseOffU:]
	if i := bytes.Index(uBase, []byte{0, 0}); i >= 0 {
		uBase = uBase[:i]
	}
	if got := utf16From(uBase); got != exe {
		t.Errorf("LocalBasePathUnicode = %q, want %q", got, exe)
	}
	// Empty suffixes are exactly one NUL / one wide NUL at their offsets.
	if suffixOffU+2 != liSize {
		t.Errorf("LinkInfoSize = %d, suffixUnicode at %d (+2 must equal size)", liSize, suffixOffU)
	}
	if suffixOffA != localBaseOffA+len(ansiBase)+1 {
		t.Errorf("CommonPathSuffixOffset = %d, want right after ANSI base NUL", suffixOffA)
	}
	if suffixOffU != localBaseOffU+len(uBase)+2 {
		t.Errorf("CommonPathSuffixOffsetUnicode = %d, want right after Unicode base NUL", suffixOffU)
	}

	// --- StringData (§2.4, ABNF order) ------------------------------
	pos := off + liSize
	readString := func() string {
		n := int(binary.LittleEndian.Uint16(data[pos:]))
		s := utf16From(data[pos+2 : pos+2+2*n])
		pos += 2 + 2*n
		return s
	}
	if got := readString(); got != target.Description {
		t.Errorf("NAME_STRING = %q", got)
	}
	if got := readString(); got != workdir {
		t.Errorf("WORKING_DIR = %q", got)
	}
	if got := readString(); got != exe {
		t.Errorf("ICON_LOCATION = %q", got)
	}
	if pos != len(data) {
		t.Errorf("trailing bytes: consumed %d of %d", pos, len(data))
	}
}

func TestWriteShellLinkRejectsEmptyTarget(t *testing.T) {
	if err := WriteShellLink(filepath.Join(t.TempDir(), "x.lnk"), ShortcutTarget{}); err == nil {
		t.Error("empty ExePath must be rejected")
	}
}
