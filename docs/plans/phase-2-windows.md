# Phase 2 — Windows x64 platform (Spec Milestone 4)

**Status:** ✅ Implemented (2026-10-04) — `make check` green; windows-tagged
tests compile via `GOOS=windows go build/vet ./...` (execution deferred to the
Phase 5 Windows CI runner, as planned). Decision record below.
**Spec:** §11 (Platforms), §21 (Windows), §16 (system-managed toolchains), §18 (platform abstraction), §33 (security), §39 (failure UX)
**Depends on:** Phase 1 (same services are exposed; TUI optional for the MVP of this phase — CLI must work, TUI inherits it automatically)

## Build decision (resolved 2026-10-04, user-approved)

Upstream ships `build.cmd` + `build.ps1` at the top level of the source
tree (present in the released `v1.3.18.6` zip). Investigation shows
they are **thin wrappers, not a bash-free path**: `build.cmd` loads
`vcvars64.bat` via `vswhere.exe` then runs `build.ps1`, which requires
Git for Windows (`sh.exe build.sh`, prepending `…\Git\bin` to PATH).
Using them as the entry point would make Git Bash a hard build
prerequisite — forbidden for the stable flow (spec §9: "must not
require a third-party package manager simply to run"; §14: stable
install must not require Git).

**Decision: option (b), fully direct.** On Windows the builder runs
the exact two commands build.sh runs for users:

```text
cmake -B <src>/build -S <src> -G Ninja -DCMAKE_BUILD_TYPE=RelWithDebInfo \
      -DLAF_BACKEND=skia -DSKIA_DIR=<skia> -DSKIA_LIBRARY_DIR=<skia>/out/Release-x64
cmake --build <src>/build -- aseprite
```

inside a `vcvars64.bat`-initialized environment. No bash anywhere.
Upstream's `build.cmd` remains the authoritative *detection contract*:
`vswhere.exe` lives at
`%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe`,
the required component ID is
`Microsoft.VisualStudio.Component.VC.Tools.x86.x64`, and the env
loader is `VC\Auxiliary\Build\vcvars64.bat`. Mirroring those strings
mirrors upstream's own discovery logic; it is not a version matrix.

The contract markers are derived from the extracted tree (`build.cmd`,
`build.ps1`, the exe/data output rules in `src/CMakeLists.txt`), so an
upstream that drops or reshapes its Windows entry points fails safely
(`UnknownContractError`) instead of being built blind. `contractLevel`
is bumped 1 → 2 because the recognized contract now carries
platform-level semantics (the fingerprint gains an OS token so state
can distinguish generations/platforms).

## Goal

`aseprite-builder install / launch / update / repair / rebuild /
doctor / uninstall` works end-to-end on Windows 11 x64, compiling
Aseprite locally with the Microsoft toolchain, installing user-locally,
and never touching an unowned existing Aseprite.

## Scope

### 1. Platform implementation — `internal/platform/windows`

- `//go:build windows` file(s) implementing `platform.Platform`
  (mirror the darwin implementation's structure: transactional install
  with staging + ownership marker + "previous" retention + rollback).
- Add `windows: {"amd64"}` to `platform.SupportedTargets()` and a
  `platformselector.selector_windows.go`. `internal/cli` already
  rejects unsupported OSes with a friendly message — extend the tests
  for the now-supported case.
- Detect (§21): Windows build number, arch, Visual Studio / Build Tools
  (via `vswhere.exe`), the Desktop C++ workload, required Windows SDK,
  free disk space (threshold comparable to darwin's 10 GiB guard).
  All **detection only** — never shipped, never installed by us.
- Install destination: `%LOCALAPPDATA%\Programs\Aseprite\` (spec §21).
  Start-Menu shortcut: **in scope** (user-approved 2026-10-04) via a
  minimal stdlib Shell Link (`.lnk`) writer (documented minimal format:
  header + LinkInfo with LocalBasePath/WorkingDirectory/icon, no
  IDList). Created on install, rewritten on update, removed on
  uninstall — only ever this builder's own `.lnk` at the fixed
  `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Aseprite.lnk` path.
- Launch through the installed `aseprite.exe`; uninstall only
  marker-proven installations (reuse darwin's marker-in-resources
  approach; for a non-bundle layout the marker lives next to the
  executable).

### 2. Build contract on Windows — the phase's central decision

Upstream's entry point is `build.sh` (bash), which is not present on a
stock Windows machine. The two options, to be weighed **before
implementation**:

- **(a) Orchestrate the upstream flow through bash from Git for
  Windows** only when already present — adds an implicit requirement
  most users won't have; risks becoming a de-facto package-manager
  dependency (spec §9 forbids requiring a package manager to run).
- **(b) Drive the documented upstream contract directly on Windows**
  (CMake + Ninja generator + VS toolchain, `LAF_BACKEND`/Skia
  environment as the upstream scripts would set them), with the
  *contract* derived from the extracted tree: mark the recognized
  Windows shape in `internal/aseprite/contract.go`, fail safely
  (`UnknownContractError`) when markers shift, and bump `contractLevel`
  (new platform-level contract semantics; state must be able to
  distinguish generations).

Option (b) is the expected outcome (spec §9 allows reimplementing the
upstream flow only with a strong technical reason — "no bash on stock
Windows" is that reason), but the implementation must stay *small* and
isolated in `internal/aseprite/build.go` platform branches + `contract.go`
markers, mirroring build.sh's own steps, never duplicating its
dependency matrix.

### 3. Skia resolution on Windows

Today `DiscoverSkiaRequirement` *executes* `laf/misc/skia-url.sh` from
the extracted source (bash). Windows needs a bash-free path that still
**derives the URL from upstream data** rather than a matrix:

- Preferred: parse the URL template out of `skia-url.sh` (read the
  script as the contract fact) and substitute tag/CPU/OS, verifying the
  derived URL against the aseprite/skia release asset list at runtime
  (GitHub API). If the template can't be parsed → fail safely.
- Skia download/digest/verify/extraction logic (`dependencies.SkiaManager`)
  is platform-neutral already — reuse as-is. One addition: the Skia
  layout validator gains the Windows library name (`skia.lib`) alongside
  `libskia.a`.

**Resolution note** (verified against upstream `skia-url.sh`): the
script's URL shape is fixed and parameterized only by the tag from
`skia-tag.txt`, the build type and the CPU naming from `platform.sh`
(`is_win = 1, cpu = x64` on MINGW/MSYS). The bash-free path parses the
template lines out of the script itself (asset name and URL skeleton),
substitutes `x64`/`windows`/tag, and validates via the aseprite/skia
release asset list. The bash *execution* path is retained for
macOS/Linux.

### 4. Windows CI test capability

Unit tests use `runner.FakeRunner`; add windows-shaped canned results.
Platform tests may run on a real Windows runner in CI only after Phase 5
(keep this phase CI-free, rely on the shared unit suite running on macOS
hosts for everything tagged testable cross-platform, plus targeted
`windows`-tagged tests executed locally/windows CI later).

## Build orchestration details (from build.sh, verified)

The Windows branch mirrors build.sh's own user-mode steps and nothing
more:

1. build.sh runs `cmake -B "$active_build_dir" -S "$source_dir" -G
   Ninja -DCMAKE_BUILD_TYPE=$build_type
   [-DCMAKE_OSX_DEPLOYMENT_TARGET=11.0 only on macOS] -DLAF_BACKEND=skia
   -DSKIA_DIR="$skia_dir"
   -DSKIA_LIBRARY_DIR="$skia_library_dir"` — for users
   `active_build_dir` is `<source>/build`, `build_type` is
   `RelWithDebInfo`, and on Windows
   `skia_library_dir=$skia_dir/out/Release-$cpu` (Debug builds would
   need `out/Debug-x64`; the builder only builds release — matching
   darwin behavior).
2. build.sh runs `cmake --build "$active_build_dir" -- aseprite`.
3. Output contract (Windows): `<source>/build/bin/aseprite.exe` and
   the data copy target `<source>/build/bin/data` — the same
   `copy_data` target macOS uses with DATA_OUTPUT_DIR switched to
   `bin/data` (no bundle indirection).

The upstream build environment is provided by `vcvars64.bat`
(captured via `cmd /c call … && set`, parsed into `KEY=VALUE`
overrides). No bash. The `.build/` config seeding and the Skia
`.deps` symlink from the darwin path are **not** used on Windows —
Skia is passed explicitly via `-DSKIA_DIR`, and Windows symlinks
would need privileges.

## Non-goals

- No MinGW path (spec §21 explicit).
- No Visual Studio installer automation in this phase (spec permits
  runtime-resolved automation; that lands with Phase 5's release/safety
  work if pursued at all).
- No administrator elevation; no registry uninstall entries unless
  trivially safe.
- No Windows TUI polish beyond "it renders and works".

## Tests

- `FakeRunner`-driven platform tests mirroring the darwin suite
  (transactional install steps, marker enforcement, retention,
  rollback, uninstall ownership), authored so they also run on macOS
  hosts where possible (pure filesystem/logic tests) with
  `//go:build windows` only where OS-specific.
- Windows contract inspection: fake trees lacking/mutating markers →
  `UnknownContractError`; derived Skia URL validation (httptest).
- Installer path handling: spaces, long paths; archive extraction for
  the zip source (already implemented — extend tests to Windows paths).
- Windows tool download resolution: CMake `cmake-*-windows-x86_64.zip`
  and Ninja `ninja-win.zip` asset matching against fake GitHub
  releases (httptest), plus `.exe` binary locating in cached
  distributions.

## Definition of done

- A user on Windows 11 x64 follows the §13 flow and receives a working
  `%LOCALAPPDATA%\Programs\Aseprite` install; no build-system knowledge
  needed; failing system checks give the §39-style actionable message.
- Updates retain the working install on failure; uninstall removes only
  owned programs + confirmed caches.
- `SupportedTargets`/docs/AGENTS.md updated; README "supports Windows";
  plan status table updated.

**Status against this list (2026-10-04):** implemented and unit-tested;
supported. All elements above are covered by the FakeRunner/httptest
suite except *actually running the pipeline on a Windows host* — that is
explicitly deferred to the Phase 5 Windows CI runner per the phase's
CI posture ("Windows CI test capability"). CLI install/launch/update/
rebuild/repair/doctor/uninstall all work through the same `internal/app`
services (`TestInstallWindowsShape` exercises the full pipeline shape).
