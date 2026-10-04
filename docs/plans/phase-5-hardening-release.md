# Phase 5 — Release engineering & hardening (Spec Milestone 7)

**Status:** ✅ Implemented (2026-10-04) — `make check` green on macOS;
`GOOS=linux|windows go vet ./...` clean; linux-tagged suite green in a
container; release artifacts built and checked locally; CI workflows
written and guarded by `internal/archtest`. The real-host E2E checklist
is documented (DEVELOPMENT.md) and is executed by the owner before the
first tag (decision: no Aseprite build in CI).
**Spec:** §3/§36 (release artifacts), §37 (testing/CI), §38 (release safety), §14 (development channel), §24/§25 (rollback/repair), §28 (doctor), §30 (CLI), §33 (security), §41 (advanced mode)
**Depends on:** Phases 1–4 (all platform slices + TUI implemented)
**Sequencing:** two PRs — (1) release + CI + §38 release-safety; (2) dev channel + advanced options + repair breadth + doctor + security review. Real-host E2E checklist lands with (2).

## Goal

Turn the working builder into a *releasable* product: safe, tagged,
artifact-hygienic releases produced by CI, plus the advanced/robustness
items Milestones 1–6 deferred, and the real-host end-to-end
verification Phases 2–4 left open.

## Context: already done — do not re-plan

Milestone 7 was marked "partial", but several items are, in fact,
already implemented and tested. This phase only closes the remaining
gaps. Confirmed in code:

- ✅ Download retry/backoff + SHA-256 verification (`internal/download`,
  all four fetchers pass the asset digest; `httptest`-tested).
- ✅ Archive traversal hardening (`internal/archive` rejects absolute
  paths, `..`, Windows separators, escaping symlinks; fixtures exist).
- ✅ Cancellation (signals wired in `main.go`; partial files never
  trusted; runner honors ctx).
- ✅ Config wired (`KeepWorkspace`/`Verbose` flow into the pipeline via
  `internal/app/config.go`; settings/advanced surface exists).
- ✅ Version-injection seam (`main.version` + `-ldflags "-X main.version=…"`).
- ✅ Rollback/retention; repair (rebuild-on-broken) — *breadth* is the gap.
- ✅ Advanced: keep-workspace, verbose, clean-rebuild, pinned `--version`.

## Decisions (2026-10-04)

1. **No full Aseprite build in CI.** CI runs fast checks only
   (lint/test, release-safety, a `doctor --json` smoke in an isolated
   `HOME`). The real-host compile+install verification is a documented
   manual checklist (see item 8) — no `workflow_dispatch` build job.
2. **CI-native releases, version from git tag.** A tag-triggered
   workflow builds native binaries on each OS runner, injects the
   version from the git tag via ldflags, runs the §38 checker, and
   attaches artifacts to the GitHub Release. `make release` stays a
   local cross-compile convenience for quick checks.
3. **Dev channel reuses the pin flag.** `install --channel dev
   --version <sha>`: `--channel` disambiguates the meaning of
   `--version` (stable → release tag, dev → git SHA). No new `--commit`
   flag.
4. **All four remaining §41 options** are implemented now (custom
   install path, source retention, system-vs-cached tool preference,
   build retention), not deferred.

## Implementation record (2026-10-04)

Shipped in four commits plus this docs pass:

- **Release engineering:** `internal/release` (the §3 target matrix,
  reproducible tar.gz/zip packs, `Version` from `VERSION` or the
  nearest git tag, `NormalizeVersion`), `internal/releasecheck` (the
  §38 forbidden-family classifier plus an exact payload allowlist —
  release archives contain only the builder executable),
  `cmd/release` (`-os/-arch/-dist` filters for CI),
  `cmd/releasecheck`, and `make release`/`dist`/`release-check`.
  Verified locally: all four artifacts build, the darwin binary reports
  the injected version, and the checker rejects a planted source
  archive.
- **CI:** `.github/workflows/ci.yml` (format/vet/tests on
  ubuntu/macos/windows, a built-binary `doctor --json` smoke, the
  release-safety job) and `.github/workflows/release.yml`
  (tag-triggered, native per-OS builds with a merged-artifact re-check,
  published through `gh release`). `internal/archtest` guards both
  workflows' required jobs.
- **Dev channel:** `aseprite.ChannelDev`, `Release.Commit` +
  `BuildVersion`, `ValidCommit`/`ShortCommit`, `GitCommitSource`
  (git probe first so a missing Git never deletes a checkout;
  blob-filtered clone, detached checkout, recursive submodules; reuse
  when HEAD matches), app-side validation/state (`CommitSHA`),
  `update` refusing dev installs with guidance, CLI `--channel dev
  --version <sha>`, and the TUI settings toggle + home commit prompt.
- **Advanced options:** `state.Config.{InstallDir,KeepSources,
  ToolPreference,RetainBuilds}` + per-run flags
  (`--install-dir`, `--keep-sources`, `--tool-preference`,
  `--retain-builds=false`) + TUI settings. Interpretations:
  build retention is an opt-out of the one-generation retention
  (dropped **after** activation, so a failed activation still rolls
  back); source retention skips pruning older trees (the active tree
  was always kept). The selector bug hiding all pointer-receiver
  capabilities (`SetAllowReplaceUnowned` silently inert in production)
  was fixed and guarded by a test.
- **Repair/doctor:** marker/version consistency, stale-previous-record
  cleanup, `platform.Repairer` (stale staging everywhere; Linux symlink
  / desktop entry / icon recreation), doctor "Build contract" and
  "CLI integration" rows feeding the report status.
- **Security:** `paths.DownloadFile` sanitizes both components
  (remote filenames are untrusted; traversal can never escape the
  downloads dir), broadened env-var redaction fixtures (JWT added),
  an archtest that confines `os/exec` to `internal/runner` and bans
  shell-string execution, and a tar.gz absolute-path fixture.
- **E2E checklist:** DEVELOPMENT.md "Real-host end-to-end
  verification (manual)".

The DoD items map as follows: `make release` + §38 checker ✅;
CI tag builds/publishes ✅ (first tag still pending); manual E2E
checklist exists ✅ (executed by the owner before the first tag — this
is the one intentionally manual item, per decision 1); docs updated ✅
(README, DEVELOPMENT, ARCHITECTURE, plans README, this file).

## Work items

### 1. Release engineering (§3/§36/§38)

- `internal/releasecheck`: a pure `Check(tree) []Violation` that walks
  an artifact tree and fails on any entry name/path matching a
  forbidden family (Aseprite binaries, Skia packages, CMake/Ninja
  distributions, VS/SDK installers, source archives, runtime caches).
  Table-driven tests construct fake pass/fail trees and run the checker
  (not the packer); these run in `make check`.
- `cmd/releasecheck`: a thin `main` that walks a directory and exits
  non-zero on violation. `make release` runs it over `dist/` before
  publishing; the CI release-safety job runs it over a dry-run tree.
- `make release` (local convenience): cross-compile the four §3
  artifacts into `dist/`, injecting `main.version` from
  `git describe --tags --abbrev=0` (or an explicit `VERSION=`
  override). `make dist` becomes an alias with real meaning.
- Reconcile `docs/DEVELOPMENT.md` (claims `dist/`) with the Makefile
  (root binary) — Makefile is the truth; fix the doc and make `dist/`
  the release target's home.
- Tag/tagging steps + downloading-from-GitHub-Releases instructions in
  README.
- Audit `.gitignore` against §36 (it already lists the §36 families;
  verify `dist/` and the release checker's own scratch paths are
  covered).

### 2. CI matrix (§37)

- `.github/workflows/ci.yml`:
  - `lint+test` on ubuntu + macos + windows runners (runs the
    OS-tagged suites on their native OS, which the local `make check`
    can't).
  - `release-safety` job: the §38 checker over a dry-run artifact tree.
  - `smoke` job: build the binary, then
    `HOME=$(mktemp -d) ./aseprite-builder doctor --json` (no secrets,
    no cache writes outside the runner temp, no Aseprite build).
- Workflow-lint test: parse `ci.yml` for the required jobs so the
  matrix can't silently rot (like `internal/archtest` guards layering).
- Document branch protection (owner action) in DEVELOPMENT.md.

### 3. Development channel (§14)

- Add `aseprite.ChannelDev` + a git-based source provider that checks
  out an explicit commit. `ParseChannel` accepts `dev`; stable stays
  the default and never requires Git.
- `install --channel stable` (validated) and
  `install --channel dev --version <sha>`. Dev without Git → a clear
  typed error; no silent degradation.
- State records the channel and commit (`InstallState.CommitSHA`);
  `Config.Channel` already exists.
- TUI exposes the channel toggle only after the backend exists (today
  the settings screen shows "dev arrives later").

### 4. Advanced options (§41) — the remaining four

- Custom install path: a platform install-root override (`--install-dir`
  CLI flag + TUI text box with validation).
- Source retention: keep the extracted source tree after install
  (mirrors `KeepWorkspace`, but for source) so rebuilds skip the
  re-download.
- System-vs-cached tool preference: a stored preference resolved at
  install time (prefer system CMake/Ninja or prefer the cached
  download), surfaced without cluttering the default flow.
- Build retention: retain the prior build output across updates
  (bounded; spec §24 forbids unlimited retention).

### 5. Repair breadth (§25)

Extend repair beyond rebuild-on-broken, each action explicit and
logged:

- marker/version mismatch (ownership marker vs. recorded version),
- previous failed update retained in `.aseprite-builder/previous`
  (cleanup),
- broken symlink in the bundle (Linux CLI symlink),
- missing desktop entry/symlink (Linux).

### 6. Doctor additions (§28)

- "Build contract" row when a cached source tree exists (inspect the
  tree the way the pipeline would, cheaply).
- "CLI integration" row on Linux (symlink + desktop-file validation).

### 7. Security review (§33)

- Broaden the env-var redaction fixture list in `internal/logging`.
- Remote-filename sanitization for the source archive name derived from
  GitHub metadata.
- A grep-level "no shell string interpolation" test (like the archtest
  layer guard).
- Extend archive adversarial fixtures where gaps exist (both zip and
  tar.gz: absolute paths, `..`, symlink escape).

### 8. Real-host E2E — documented manual checklist (deferred from Phases 2–4)

- A checklist in DEVELOPMENT.md (or a `docs/` page) closing the
  "real-VM E2E pending" notes in the plans-README rows for milestones
  2/4/5/6: run the tagged artifact on macOS (native), Windows (native),
  and Linux (container/VM) → `install` → compile Aseprite → `launch` →
  `update` → `uninstall`, plus `doctor` before/after.
- Not automated in CI (decision 1).

## Non-goals

- No new platforms (Windows/Linux phases carried those).
- No TUI restructure.
- No scheduled/unattended auto-update (spec never asks).
- No full Aseprite compile in CI.

## Tests

All FakeRunner / `httptest` / real-temp-dir; never compile Aseprite,
never touch the network.

- §38 checker pass/fail fixtures (forbidden names, nested paths);
  packer output-tree snapshot for the §3 naming; ldflags version
  reported by the `version` command.
- CI workflow-lint test (parse `ci.yml`, assert required jobs).
- Dev-channel matrix: stable default, explicit dev requires git, bad
  channel/commit → typed error.
- Advanced options: install-dir validation (reject paths outside
  writable roots / nested-in-managed dirs), source-retention and
  build-retention behavior, tool-preference resolution.
- Repair-breadth cases: each new detection produces the right explicit
  action + log.
- Security: broadened env-redaction, remote-filename sanitization,
  no-shell-interpolation grep test, added archive fixtures.

## Definition of done

- ✅ `make release` produces spec-§3 artifacts that the §38 checker
  accepts (verified locally); `.github/workflows/release.yml` builds
  them on tag and attaches them to a GitHub Release (first tag
  pending).
- ✅ Spec §44: every implemented platform row has code and tests behind
  it; the real-host runs are the manual checklist.
- ✅ The real-host E2E checklist exists in DEVELOPMENT.md and is
  executed on each supported platform before the first tag (manual by
  decision 1).
- ✅ README status, DEVELOPMENT (release, branch protection, manual
  E2E), ARCHITECTURE (source channels, repair/diagnostics/advanced
  preferences) and the plans README (milestone 7 row) updated.
  AGENTS.md needed no change: the release tooling is ordinary
  in-module Go and preserves every listed rule.
