// Package selector chooses the distribution package backend for the
// host from /etc/os-release (spec §22): a distribution that identifies
// with a known family is handled by that family's backend, and an
// ambiguous or unknown release falls back to probing the known manager
// binaries before failing safely.
//
// The package is host-neutral (the backends probe binaries by path),
// so the selection matrix is testable anywhere; the Linux platform
// supplies the real manager set and runner.
package selector

import (
	"context"

	"github.com/akalp/aseprite-builder/internal/packages"
)

// fallbackProbeOrder is used when os-release does not identify a
// supported family: every backend's binaries are probed in this order.
var fallbackProbeOrder = []packages.ManagerName{
	packages.ManagerApt,
	packages.ManagerDnf,
	packages.ManagerPacman,
	packages.ManagerZypper,
	packages.ManagerXbps,
}

// Candidates returns the backend families to try for rel, in order. A
// recognized distribution yields its family (or families); an
// ambiguous or unknown release yields every supported family so their
// binaries can be probed.
func Candidates(rel packages.OSRelease) []packages.ManagerName {
	var out []packages.ManagerName
	if rel.IsDebianLike() {
		out = append(out, packages.ManagerApt)
	}
	if rel.IsFedoraLike() {
		out = append(out, packages.ManagerDnf)
	}
	if rel.IsArchLike() {
		out = append(out, packages.ManagerPacman)
	}
	if rel.IsSuseLike() {
		out = append(out, packages.ManagerZypper)
	}
	if rel.IsVoidLike() {
		out = append(out, packages.ManagerXbps)
	}
	if len(out) == 0 {
		out = append(out, fallbackProbeOrder...)
	}
	return out
}

// Manager selects the backend for rel: the first candidate whose
// implementation detects wins. available maps manager names to their
// implementations; nil entries are skipped. ErrUnsupportedDistro is
// returned when nothing applies — unsupported distributions fail
// safely rather than being guessed at (spec §11).
func Manager(ctx context.Context, rel packages.OSRelease, available map[packages.ManagerName]packages.Manager) (packages.Manager, string, error) {
	for _, name := range Candidates(rel) {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		mgr := available[name]
		if mgr == nil {
			continue
		}
		if mgr.Detect(ctx) {
			return mgr, string(name), nil
		}
	}
	return nil, "", packages.ErrUnsupportedDistro
}
