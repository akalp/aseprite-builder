//go:build windows

package windows

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

func TestRepairTargetedRemovesStaleStaging(t *testing.T) {
	p := newTestPlatform(t, &runner.FakeRunner{})
	staging := filepath.Join(p.ProgramsRoot, retentionDirName, "staging", installDirName)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}

	actions, err := p.RepairTargeted(context.Background(), platform.RepairRequest{})
	if err != nil {
		t.Fatalf("RepairTargeted: %v", err)
	}
	if len(actions) != 1 || !strings.Contains(actions[0], "staging") {
		t.Errorf("actions = %v, want the stale staging removal", actions)
	}
	if _, err := os.Stat(filepath.Join(p.ProgramsRoot, retentionDirName, "staging")); !os.IsNotExist(err) {
		t.Errorf("staging must be gone, stat err = %v", err)
	}

	// Idempotent.
	actions, err = p.RepairTargeted(context.Background(), platform.RepairRequest{})
	if err != nil || len(actions) != 0 {
		t.Errorf("second run = %v, %v, want nothing to fix", actions, err)
	}
}
