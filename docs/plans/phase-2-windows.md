# Phase 2 — Windows x64 platform (Spec Milestone 4)

**Status:** Planned
**Spec:** §11 (Platforms), §21 (Windows), §16 (system-managed toolchains), §18 (platform abstraction), §33 (security), §39 (failure UX)
**Depends on:** Phase 1 (same services are exposed; TUI optional for the MVP of this phase — CLI must work, TUI inherits it automatically)

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
  Optional Start-Menu shortcut via a minimal `.lnk` writer (stdlib,
  documented Shell Link format) or explicit out-of-scope note — decide
  at implementation, don't reach for a dependency for it.
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
  is platform-neutral already — reuse as-is.

### 4. Windows CI test capability

Unit tests use `runner.FakeRunner`; add windows-shaped canned results.
Platform tests may run on a real Windows runner in CI only after Phase 5
(keep this phase CI-free, rely on the shared unit suite running on macOS
hosts for everything tagged testable cross-platform, plus targeted
`windows`-tagged tests executed locally/windows CI later).

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

## Definition of done

- A user on Windows 11 x64 follows the §13 flow and receives a working
  `%LOCALAPPDATA%\Programs\Aseprite` install; no build-system knowledge
  needed; failing system checks give the §39-style actionable message.
- Updates retain the working install on failure; uninstall removes only
  owned programs + confirmed caches.
- `SupportedTargets`/docs/AGENTS.md updated; README "supports Windows";
  plan status table updated.
