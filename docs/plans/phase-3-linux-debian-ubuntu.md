# Phase 3 — Linux, Debian/Ubuntu vertical slice (Spec Milestone 5)

**Status:** Planned
**Spec:** §11 (Linux targets), §16 (Linux system-managed packages), §22 (Linux), §18 (abstraction), §33 (privilege safety)
**Depends on:** Phase 1 (frontends share the same services)

## Goal

Debian + Ubuntu (amd64) support end-to-end: system library detection
with distribution-native package handling, Aseprite compiled against
the system toolchain, user-local install (`~/.local/opt/aseprite`),
CLI/desktop integration, transactional updates, uninstall.

## Scope

### 1. Platform implementation — `internal/platform/linux`

- `//go:build linux` implementation of `platform.Platform` mirroring
  darwin's: detect via `/etc/os-release` (ID, ID_LIKE, VERSION_CODENAME),
  compiler probe (`g++`/`cc` via `cc` alternatives), disk-space check,
  transactional install to `~/.local/opt/aseprite/<version>` style
  layout with **current/previous** retention mirroring the macOS
  approach (same rollback semantics, marker file proving ownership).
- Add `linux: {"amd64"}` to `SupportedTargets()` + a linux selector.
  Decide explicitly whether arm64 enters now (spec: "arm64 where
  upstream requirements permit") — default: defer to a later follow-up,
  keep the scope Debian/Ubuntu amd64.
- Output contract: no `.app` bundle. Extend `Inspector.Inspect`
  (already parameterized by `wantsAppBundle`) with the Linux output
  markers (executable target + `data` install rule in `src/CMakeLists.txt`
  / `build.sh` skeleton). Unknown/mutated → fail safely; bump
  `contractLevel` if the marker *shape* changes.

### 2. Source/build flow reuse

- `build.sh --auto --norun` works on stock Ubuntu/Debian (bash, cmake,
  ninja in PATH; resolved via the existing `dependencies.Resolver`
  with system-first reuse — no changes expected beyond a `skiaOSName`
  mapping and `x64` CPU naming from `laf/misc/platform.sh`).
- Skia: reuse the existing source-tree discovery (`skia-url.sh`
  executed from the extracted tree works on Linux) + `SkiaManager`
  unchanged.
- Source acquisition: the Aseprite Source.zip flow is unchanged.

### 3. Packages layer — `internal/packages`

```text
internal/packages/
    packages.go   PackageManager interface (Detect / MissingPackages / Install)
    apt/apt.go    Debian/Ubuntu implementation
```

- Interface exactly as spec §22. `apt` checks presence with
  `dpkg-query`/`apt-cache` (FakeRunner-tested), never assumes versions.
- Required packages ("Desktop-Development-with-C++ equivalent" for
  Linux): compiler, cmake, ninja (+ `libx11`/`libfontconfig`/`libcurl`
  dev libs per upstream's documented system deps). The Debian *package
  names* are builder platform knowledge — keep them in one
  documented table in the apt package with rationale comments (this is
  the sanctioned exception to "no matrices": names, not versions).
- Mapping missing packages → explicit user action:

  - **Default (GUI + non-TTY + TUI): never invoke sudo.** Show the
    exact command the user can run (`sudo apt-get install …`) and
    re-check after.
  - **Explicit CLI/TUI consent path:** on a live TTY, after an
    explicit confirmation, run the visible `sudo apt-get install …`
    interactively (user watches it happen — §16 "never *silently*
    invoke sudo"). No hidden/environment-stripped sudo; no defaults
    that skip consent; no `--yes` flag auto-approves package installs
    (a `--yes` may only skip *builder* prompts).
- If the user declines, stop before source download with the friendly
  system-requirements message + fix hint (mirrors the macOS
  `SystemRequirementsError` UX).

### 4. Desktop + CLI integration (spec §22)

- `~/.local/bin/aseprite` → symlink into the installed tree
  (re-point on update; remove on uninstall; only ever this builder's
  own symlink target is touched).
- `~/.local/share/applications/aseprite.desktop` (own, marker-commented
  file; regenerated on update; removed on uninstall), icon set from the
  build's `data/icons` into `~/.local/share/icons/hicolor/**`,
  `update-desktop-database`/`gtk-update-icon-cache` best-effort + log.
- Launch via the installed executable (TTY-safe; `launch` from CLI/TUI
  must not assume a terminal emulator).

## Non-goals

- Any other distro (dnf/pacman/… is Phase 4; `zypper`/`xbps` likewise).
- No bundling of `.deb`/`.rpm` (spec §16), no apt repository, no root
  installs, no glibc-anything hacks.
- No arm64 in this slice (decide with evidence later).

## Tests

- `FakeRunner` suite mirroring darwin: detection (os-release fixture
  via temp dir injection), requirements checks, apt probe results →
  MissingPackages, consent gating (sudo never called without consent —
  assert with the fake), install staging/retention/rollback, symlink +
  desktop file lifecycle in real temp HOME dirs.
- Contract inspection with Linux marker mutations.
- Nothing network- or compile-dependent.

## Definition of done

- On a fresh Ubuntu/Debian VM: `doctor` explains exactly what to
  install; after installing system packages, `install` completes and
  Aseprite shows in the app menu; update/rebuild/repair/uninstall all
  behave like the macOS flow; failed updates never destroy the working
  install; uninstall removes only owned files.
- AGENTS.md layer graph updated with `internal/packages`;
  ARCHITECTURE.md resolution table gains a Linux row; plan table
  updated.
