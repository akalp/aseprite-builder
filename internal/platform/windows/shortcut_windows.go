//go:build windows

package windows

import (
	"os"
	"path/filepath"
	"syscall"
)

// ShortcutPath is the Start Menu shortcut this builder manages for its
// Aseprite installation. The fixed path is also the ownership proof:
// uninstall touches only this file.
func (p Platform) ShortcutPath() string {
	return filepath.Join(p.StartMenuRoot, "Aseprite.lnk")
}

// ShortcutTarget describes one .lnk to write.
type ShortcutTarget struct {
	// ExePath is the link's local target (LocalBasePathUnicode).
	ExePath string
	// WorkingDir is the "Start in" directory.
	WorkingDir string
	// IconPath locates the icon resource (the exe itself; index 0).
	IconPath string
	// Description is the NAME_STRING tooltip/comment.
	Description string
}

// Shell Link (.lnk) format constants ([MS-SHLLINK] Shell Link Binary
// File Format). This writer emits the documented subset:
//
//	ShellLinkHeader — LinkFlags = HasLinkInfo | HasName | HasWorkingDir |
//	  HasIconLocation | IsUnicode; FileAttributes = FILE_ATTRIBUTE_NORMAL.
//	LinkInfo — VolumeIDAndLocalBasePath with LinkInfoHeaderSize 0x24, so
//	  both the ANSI LocalBasePath and the authoritative Unicode
//	  LocalBasePathUnicode are present (install paths can be non-ASCII;
//	  the ANSI copy is a lossy best-effort, the Unicode one exact).
//	StringData — exact ABNF order NAME_STRING, WORKING_DIR,
//	  ICON_LOCATION (count-delimited Unicode, never NUL-terminated).
//
// No LinkTargetIDList, no TrackerData, no extra data blocks: the
// minimal form every shell resolver supports. Written with the
// standard library only (phase decision, user-approved 2026-10-04).
const (
	shellLinkHeaderSize = 0x4C

	linkFlagHasLinkInfo   = 0x00000002 // B
	linkFlagHasName       = 0x00000004 // C
	linkFlagHasWorkingDir = 0x00000010 // E
	linkFlagHasIconLoc    = 0x00000040 // G
	linkFlagIsUnicode     = 0x00000080 // H

	fileAttrNormal    = 0x00000080
	showCommandNormal = 0x00000001

	// LinkInfo: header includes the two Unicode offset fields.
	linkInfoHeaderSizeUnicode = 0x24
	linkInfoVolumeAndBasePath = 0x00000001 // A: VolumeID + LocalBasePath present

	// StringData limit per [MSLEARN-MaxPathLength]: 260 characters.
	maxLnkStringChars = 260
)

// WriteShellLink composes the minimal Shell Link file and writes it to
// path. Windows-side acceptance is covered by the windows-tagged test
// suite; the byte layout is verified by a host-agnostic round-trip
// parser test (shortcut_windows_test.go).
func WriteShellLink(path string, t ShortcutTarget) error {
	if t.ExePath == "" {
		return os.ErrInvalid
	}

	header := shellLinkHeader(t)
	linkInfo := buildLinkInfo(t.ExePath)
	stringData := append(
		unicodeString(t.Description),
		unicodeString(t.WorkingDir)...)
	stringData = append(stringData, unicodeString(t.IconPath)...)

	out := make([]byte, 0, len(header)+len(linkInfo)+len(stringData))
	out = append(out, header...)
	out = append(out, linkInfo...)
	out = append(out, stringData...)
	return os.WriteFile(path, out, 0o644)
}

// shellLinkHeader writes the 76-byte ShellLinkHeader (§2.1). Layout:
// 0 HeaderSize; 4–20 LinkCLSID; 20 LinkFlags; 24 FileAttributes; 28
// CreationTime; 36 AccessTime; 44 WriteTime (all zero = "no time set");
// 52 FileSize; 56 IconIndex; 60 ShowCommand; 64 HotKey; 66/68/72
// reserved (zero).
func shellLinkHeader(t ShortcutTarget) []byte {
	h := make([]byte, shellLinkHeaderSize)
	putLE32(h, 0, shellLinkHeaderSize)
	copy(h[4:20], guidBytes())
	linkFlags := uint32(linkFlagHasLinkInfo | linkFlagHasName |
		linkFlagHasWorkingDir | linkFlagHasIconLoc | linkFlagIsUnicode)
	putLE32(h, 20, linkFlags)
	putLE32(h, 24, fileAttrNormal)
	putLE32(h, 60, showCommandNormal)
	return h
}

// buildLinkInfo assembles the LinkInfo structure (§2.3) with the
// Unicode extensions (header size 0x24 = 9 u32 fields):
//
//	0  LinkInfoSize
//	4  LinkInfoHeaderSize = 0x24
//	8  LinkInfoFlags = VolumeIDAndLocalBasePath
//	12 VolumeIDOffset = 0 (VolumeID absent)
//	16 LocalBasePathOffset = 0x24
//	20 CommonNetworkRelativeLinkOffset = 0 (absent)
//	24 CommonPathSuffixOffset
//	28 LocalBasePathOffsetUnicode
//	32 CommonPathSuffixOffsetUnicode
//	   ANSI LocalBasePath (NUL-terminated), ANSI CommonPathSuffix
//	   (empty: one NUL), LocalBasePathUnicode (NUL-terminated),
//	   CommonPathSuffixUnicode (empty: one NUL).
func buildLinkInfo(exePath string) []byte {
	const headerSize = linkInfoHeaderSizeUnicode
	ansiBase := ansiWithNUL(exePath) // lossy ANSI copy + NUL
	suffixANSIOff := headerSize + len(ansiBase)
	unicodeBase := utf16Nul(exePath)    // exact copy + NUL
	unicodeBaseOff := suffixANSIOff + 1 // after the empty ANSI suffix NUL
	suffixUnicodeOff := unicodeBaseOff + len(unicodeBase)

	size := suffixUnicodeOff + 2 // + empty Unicode suffix NUL

	out := make([]byte, 0, size)
	out = appendLE32(out, uint32(size))
	out = appendLE32(out, linkInfoHeaderSizeUnicode)
	out = appendLE32(out, linkInfoVolumeAndBasePath)
	out = appendLE32(out, 0)                        // VolumeIDOffset
	out = appendLE32(out, uint32(headerSize))       // LocalBasePathOffset
	out = appendLE32(out, 0)                        // CommonNetworkRelativeLinkOffset
	out = appendLE32(out, uint32(suffixANSIOff))    // CommonPathSuffixOffset
	out = appendLE32(out, uint32(unicodeBaseOff))   // LocalBasePathOffsetUnicode
	out = appendLE32(out, uint32(suffixUnicodeOff)) // CommonPathSuffixOffsetUnicode
	out = append(out, ansiBase...)
	out = append(out, 0) // empty ANSI CommonPathSuffix
	out = append(out, unicodeBase...)
	out = append(out, 0, 0) // empty Unicode CommonPathSuffix
	return out
}

// unicodeString encodes one StringData item (§2.4): u16
// CountCharacters then that many UTF-16 code units, never
// NUL-terminated.
func unicodeString(s string) []byte {
	units := lnkUnits(s)
	out := make([]byte, 2+2*len(units))
	putLE16(out, 0, uint16(len(units)))
	for i, c := range units {
		putLE16(out, 2+2*i, c)
	}
	return out
}

// ansiWithNUL renders the code-page representation of the path for the
// ANSI fields. Non-ASCII runes collapse to '?' (lossy by design);
// LocalBasePathUnicode carries the exact path, so nothing downstream
// depends on the ANSI copy being readable.
func ansiWithNUL(s string) []byte {
	b := make([]byte, 0, len(s)+1)
	for _, r := range s {
		if r < 128 {
			b = append(b, byte(r))
		} else {
			b = append(b, '?')
		}
	}
	return append(b, 0)
}

// lnkUnits converts s to UTF-16 code units capped at the 260-char
// StringData limit.
func lnkUnits(s string) []uint16 {
	units, err := syscall.UTF16FromString(s)
	if err == nil && len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1] // UTF16FromString appends a NUL
	}
	if len(units) > maxLnkStringChars {
		units = units[:maxLnkStringChars]
	}
	return units
}

// utf16Nul converts s to NUL-terminated UTF-16LE bytes (LinkInfo
// Unicode strings are NUL-terminated).
func utf16Nul(s string) []byte {
	units := lnkUnits(s)
	b := make([]byte, 2*(len(units)+1))
	for i, c := range units {
		b[2*i] = byte(c)
		b[2*i+1] = byte(c >> 8)
	}
	return b
}

// --- little-endian helpers -------------------------------------------

func putLE32(b []byte, off int, v uint32) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
	b[off+2] = byte(v >> 16)
	b[off+3] = byte(v >> 24)
}

func putLE16(b []byte, off int, v uint16) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
}

func appendLE32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// guidBytes renders the mandatory LinkCLSID
// {00021401-0000-0000-C000-000000000046} in binary form (first three
// fields little-endian, per [MS-SHLLINK] §2.1).
func guidBytes() []byte {
	return []byte{
		0x01, 0x14, 0x02, 0x00, // 00021401
		0x00, 0x00, // 0000
		0x00, 0x00, // 0000
		0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46, // C000-000000000046
	}
}
