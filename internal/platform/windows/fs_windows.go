//go:build windows

package windows

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/akalp/aseprite-builder/internal/runner"
)

// freeDiskBytes reports the free bytes on the drive holding dir, via a
// PowerShell probe (unprivileged, no third-party dependency). The
// runner indirection keeps the call FakeRunner-testable on any host,
// mirroring how darwin probes sw_vers.
func freeDiskBytes(r runner.Runner, dir string) (int64, error) {
	drive := filepath.VolumeName(dir)
	drive = strings.TrimSuffix(drive, ":")
	if drive == "" {
		// A drive-less path (relative dir): probe the current drive.
		drive = "."
	}
	res, err := r.Run(context.Background(), runner.Command{
		Name: "powershell",
		Args: []string{
			"-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf("(Get-PSDrive -Name %s).Free", drive),
		},
	})
	if err != nil {
		return 0, fmt.Errorf("probe free space on %s: %w", drive, err)
	}
	if !res.OK() {
		return 0, fmt.Errorf("probe free space on %s: powershell exited with code %d", drive, res.ExitCode)
	}
	out := strings.TrimSpace(res.Stdout)
	// PowerShell may print culture-formatted numbers; accept plain
	// digits only and strip typical separators.
	out = strings.Map(func(r rune) rune {
		switch r {
		case ',', ' ', 0xa0: // comma and NBSP group separators
			return -1
		}
		return r
	}, out)
	free, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse free space output %q: %w", out, err)
	}
	return free, nil
}
