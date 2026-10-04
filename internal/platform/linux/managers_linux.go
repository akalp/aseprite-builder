//go:build linux

package linux

import (
	"context"
	"errors"

	"github.com/akalp/aseprite-builder/internal/packages"
	"github.com/akalp/aseprite-builder/internal/packages/apt"
	"github.com/akalp/aseprite-builder/internal/packages/dnf"
	"github.com/akalp/aseprite-builder/internal/packages/pacman"
	"github.com/akalp/aseprite-builder/internal/packages/selector"
	"github.com/akalp/aseprite-builder/internal/packages/xbps"
	"github.com/akalp/aseprite-builder/internal/packages/zypper"
)

// supportedDistrosFix is the remediation text shown when no package
// backend applies: the families this release understands, with their
// backends.
const supportedDistrosFix = "This release supports Debian/Ubuntu (apt), Fedora/RHEL (dnf), " +
	"Arch (pacman), openSUSE (zypper) and Void (xbps); " +
	"ensure the distribution's package manager is installed"

// availableManagers constructs the package backends the selector may
// probe. Tests replace the set through managerOverride to make
// selection deterministic on any host.
func (p Platform) availableManagers() map[packages.ManagerName]packages.Manager {
	if p.managerOverride != nil {
		return p.managerOverride
	}
	return map[packages.ManagerName]packages.Manager{
		packages.ManagerApt:    apt.Platform{Runner: p.Runner},
		packages.ManagerDnf:    dnf.Platform{Runner: p.Runner},
		packages.ManagerPacman: pacman.Platform{Runner: p.Runner},
		packages.ManagerZypper: zypper.Platform{Runner: p.Runner},
		packages.ManagerXbps:   xbps.Platform{Runner: p.Runner},
	}
}

// selectManager returns the backend for rel, or a nil manager when no
// supported backend applies. Only the unsupported-distribution error is
// swallowed (the requirement check explains it); other errors (e.g.
// cancellation) propagate.
func (p Platform) selectManager(ctx context.Context, rel packages.OSRelease) (packages.Manager, string, error) {
	mgr, name, err := selector.Manager(ctx, rel, p.availableManagers())
	if err != nil && !errors.Is(err, packages.ErrUnsupportedDistro) {
		return nil, "", err
	}
	return mgr, name, nil
}

// selectedManager reads os-release and selects the backend. A nil
// manager means the distribution has no supported backend.
func (p Platform) selectedManager(ctx context.Context) (packages.Manager, string, error) {
	rel, err := p.osRelease()
	if err != nil {
		return nil, "", err
	}
	return p.selectManager(ctx, rel)
}
