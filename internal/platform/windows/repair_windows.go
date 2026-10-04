//go:build windows

package windows

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/akalp/aseprite-builder/internal/platform"
)

// RepairTargeted implements platform.Repairer: it removes staging left
// behind by an interrupted update. Only builder-owned paths are
// touched; the active installation is never modified here (that is the
// rebuild path).
func (p Platform) RepairTargeted(_ context.Context, _ platform.RepairRequest) ([]string, error) {
	staging := filepath.Join(p.ProgramsRoot, retentionDirName, "staging")
	if _, err := os.Stat(staging); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	return []string{"removed staging left by an interrupted update"}, nil
}
