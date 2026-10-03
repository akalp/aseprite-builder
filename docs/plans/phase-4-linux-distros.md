# Phase 4 — Linux, additional distributions (Spec Milestone 6)

**Status:** Planned
**Spec:** §11, §16, §22
**Depends on:** Phase 3 (the linux platform + packages layer exists)

## Goal

Add `dnf`, `pacman`, `zypper`, `xbps` behind the
`packages.PackageManager` interface built in Phase 3, so Fedora, Arch,
openSUSE and Void users get the same experience as Debian/Ubuntu
without touching the platform or app layers.

## Scope

Per package-manager module (one file + one test file each):

| Module | Detect | Presence probe | Install (explicit-consent path only) |
|---|---|---|---|
| `dnf` | `/usr/bin/dnf` | `rpm -q <pkg> --queryformat` | `sudo dnf install <pkg…>` |
| `pacman` | `/usr/bin/pacman` | `pacman -Qi <pkg>` | `sudo pacman -S --needed <pkg…>` |
| `zypper` | `/usr/bin/zypper` | `rpm -q <pkg>` | `sudo zypper install <pkg…>` |
| `xbps` | `/usr/bin/xbps-install` | `xbps-query -S / -l <pkg>` | `sudo xbps-install -S <pkg…>` |

- Distro → manager selection via `os-release` (#ID/ID_LIKE): fedora/
  rhel/rocky/almalinux → dnf, arch/manjaro → pacman, opensuse → zypper,
  void → xbps; fall back to probing manager binaries when os-release is
  ambiguous.
- Same consent rules as Phase 3 (instruction-by-default, visible
  interactive sudo only after explicit user consent on a TTY, never
  silently, never from `--yes`).
- Per-family package-name tables (names only, never versions), one
  documented location, verified at runtime before use.
- Update the linux platform's `Requirements` flow to compose with the
  selected manager; keep `SystemRequirementsError` UX shared.
- Minor platform adjustments that surface here (e.g. logind/Flatpak
  sandbox false-positive guidance) happen only if the real-VM smoke
  checklist demands them — speculative support is not in scope.

## Non-goals

- Immutable-distro / container flatpak/snap distributions.
- Re-deciding Phase 3 behaviors (consent model, install layout).

## Tests

- Per-manager `FakeRunner` probes and command-construction tests.
- os-release matrix tests (fixture files, real temp dirs).
- Selection fallback tests (ID_LIKE chains, absent os-release → probe
  binaries → otherwise fail safe with a clear message).

## Definition of done

- Every manager can report missing system packages and hand the user a
  single copy-paste command; after the user runs it, the Phase 3 flow
  completes unchanged.
- README wording follows spec §11 ("Supports Aseprite's major desktop
  build platforms", not "every OS"); docs and the plan table updated.
