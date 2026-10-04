//go:build linux

package linux

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// freeDiskBytes reports available bytes for unprivileged users on the
// filesystem holding dir.
func freeDiskBytes(dir string) (int64, error) {
	// Statfs on a missing directory fails; probe the nearest existing
	// ancestor (typically the user's home).
	probe := dir
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(probe, &stat); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", probe, err)
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
