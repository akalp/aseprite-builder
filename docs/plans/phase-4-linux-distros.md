# Phase 4 — Linux, additional distributions (Spec Milestone 6)

**Status:** ✅ Implemented (2026-10-04) — `make check` green on macOS;
`GOOS=linux GOARCH=amd64 go vet ./...` clean; the linux-tagged suite green in a
Linux container; package names verified against the real Fedora, Arch, openSUSE
and Void repositories. Real-VM smoke E2E deferred to the Phase 5 CI runner, as
planned. Decision record below.
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

## Decisions (2026-10-04)

1. **Selection lives in `internal/packages/selector`.** The os-release
   family matrix, the ambiguous-release probe order and the first-hit
   probing are host-neutral, so the phase's central logic is unit-tested
   on every host (not only in the Linux container). The package is named
   `selector` because `select` is a Go keyword. The linux platform only
   supplies the constructed manager set and its runner.
2. **`packages.Manager` extends `packages.PackageManager`** (spec §22) with
   `RequiredPackages() []Package` and `InstallCommand([]Package) string`.
   All five backends implement it; apt's package-level functions stay and
   the new methods delegate, so instruction and consented run cannot
   drift. The linux platform composes `Requirements`/`SystemPackages`
   from the selected manager instead of naming apt.
3. **`Detect` is a pure binary-presence check.** Family gating moved
   entirely into the matrix, which is what lets an ambiguous os-release
   fall back to probing binaries. A recognized family never falls back to
   a different manager (e.g. Fedora without dnf fails safely).
4. **Presence probes are per-package exit-status checks** through the
   shared `packages.MissingByExitStatus` helper (`rpm -q <pkg>`,
   `pacman -Qi <pkg>`, `xbps-query <pkg>`; apt keeps its batched
   `dpkg-query`). A probe that cannot start is a real error; a non-zero
   exit is simply "missing". The plan doc's `xbps-query -S / -l <pkg>`
   was garbled and is corrected to `xbps-query <pkg>`.
5. **Package names** are the sanctioned names-only tables: Fedora/RHEL
   (`gcc-c++`, `ninja-build`, `mesa-libGL-devel`, `libX*-devel`, …),
   Arch (`gcc`, `ninja`, `libglvnd`, `libx*`, no `-dev` suffix), openSUSE
   (`gcc-c++`, `ninja`, `Mesa-libGL-devel`, `libX*-devel`), Void (`gcc`,
   `ninja`, `MesaLib-devel`, `libX*-devel`). `clang` is kept in every
   table (upstream's documented Debian set; the AUR package also requires
   it). Names were verified against the real repositories (Fedora/Arch/
   openSUSE containers; the official Void package index — Void's own
   container has an xbps fetch bug, and `clang`/`libX11-devel`/… are
   subpackages, so srcpkgs checks were insufficient).
6. **Install commands** stay exactly what the user is shown — no
   auto-confirm flags beyond pacman's `--needed` and xbps's `-S`, which
   only skip already-satisfied work, never a confirmation. Execution goes
   through the shared visible `packages.RunSudo` (terminal attached, no
   output capture).
7. **The linux platform keeps a test-only `managerOverride` seam** so its
   tests are deterministic regardless of the host distribution (the
   previous tests assumed /usr/bin/apt-get existed).
8. **No app/CLI/TUI changes**: they already consume `packages.Report` and
   `[]packages.Package`, which is the payoff of the Phase 3 consent
   architecture.

**Status against the DoD:** all managers report missing packages and the
exact command; read-only `doctor` was exercised in real Fedora 44, Arch,
openSUSE Leap 16 and Void containers, showing the right backend and
command each time. The "after the user runs it, install completes"
half is the real-VM E2E, deferred to Phase 5 like the Phase 2/3 smoke.
