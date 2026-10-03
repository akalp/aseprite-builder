# Phase 5 — Release engineering & hardening (Spec Milestone 7)

**Status:** Planned
**Spec:** §3/§36 (release artifacts), §37 (testing strategy/CI), §38 (release safety), §14 (development channel), §24/§25 (rollback/repair), §30 (CLI surface), §33 (security), §41 (advanced mode), §28 (doctor)
**Depends on:** any time; safe to run after Phase 1; re-verify after each platform phase

## Goal

Turn the working builder into a *releasable* product: safe, tagged,
artifact-hygienic releases on a CI matrix across platforms, plus the
advanced/robustness items the first Milestones 1–4 deferred.

## Work items

### A. Release engineering (§3, §36, §38)

- `make release` (target + `scripts/release*` stdlib shell or a small
  Go helper, no new deps): builds per-`GOOS/GOARCH` binaries, packs
  them into the exact §3 layout
  (`aseprite-builder_Darwin_arm64.tar.gz`, `_x86_64.tar.gz`,
  `_Windows_x86_64.zip`, `_Linux_x86_64.tar.gz`) into a `dist/`.
- Version injection: `-ldflags "-X main.version=1.0.0"`; `version`
  must stop reporting `0.1.0-dev` in tagged artifacts.
- **Release-safety checker** (spec §38, an automated gate): a Go test
  + script that walks the produced artifact tree and fails if any
  entry name/path matches forbidden families (Aseprite binaries, Skia
  packages, CMake/Ninja distributions, VS/SDK installers, source
  archives, runtime caches). Table-driven tests assert both pass and
  fail cases (construct fake artifact trees, run the checker, not the
  packer).
- Tag/tagging docs + downloading-from-GitHub-Releases instructions in
  README; note.alignment with the mocked release-artifact tree above.
  The `.gitignore` already covers caches; audit it against §36.
- Reconcile `docs/DEVELOPMENT.md` (`dist/`) with the Makefile (root
  binary) — AGENTS.md documents the Makefile as the truth; fix the
  doc, also give `make dist` a real meaning if release targets adopt
  `dist/`.

### B. CI matrix (§37)

- GitHub Actions (`.github/workflows/ci.yml`):
  - `lint+test` on ubuntu + macos + windows runners (for the phase-2/3+
    build-tagged suites that only run on their OS).
  - Windows/Linux/macOS **unit-test-only** jobs: never build Aseprite.
  - Release-safety job (checker over a dry-run artifact tree).
  - Optional smoke job: run the built binary with `HOME=$(mktemp -d)`
    against `doctor --json` (isolated via AGENTS.md's documented trick;
    no secrets, no cache writes outside the runner temp).
- Gates: branch protection on `make check` + CI green (project owner's
  responsibility; document it in DEVELOPMENT.md).

### C. Download & cancellation hardening (§32, §33)

- Digest coverage: cmake/ninja fetchers must use GitHub asset digests
  when present (Skia + source already do; audit + tests for the
  toolchain fetchers); log the verification level honestly like the
  source does today.
- Retry/backoff policy for downloads (bounded, logged), tested with
  `httptest` failure injection.
- Cancellation tests: cancel mid-download and mid-build → partial
  temp files never become cache entries; in-flight install leaves the
  previous installation intact; runner context honoring covered by
  tests.
- Security review pass over §33: untrusted-path extraction (already
  has traversal protection — add adversarial fixture tests with
  absolute paths, `..`, symlink escape archives), remote-filename
  sanitization, env-var redaction coverage (logging already redacts —
  broaden the fixture list), no shell string interpolation anywhere
  (grep-level test).

### D. Advanced surface (§14, §30, §41) — the deferred spec items

- **Development channel**: git-based, explicit commit Aseprite source;
  `install --channel stable` (already implicitly stable — add the flag
  + validation), `--channel dev` requires Git and reports clearly when
  absent; stable remains the default everywhere; state records
  channel/commit; TUI exposing it only after backend exists.
- **Settings from config**: `state.Config` is currently dead weight —
  either wire it (KeepWorkspace/Verbose into the pipeline; Phase 1
  already includes this) or remove it with docs; decide from evidence
  when implemented, don't keep both an unused struct and a flag set.
- **Advanced options** (§41) — implement the ones with a concrete
  user demand first, e.g. keep-workspace default, verbose logs,
  source retention, custom install path (platform option; TUI "text
  box + validate"), system-vs-cached tool *preference* metadata only.
  Non-abstracted: implement per spec §41 the actual list above but
  stagger it (pin: verbose + retention + custom path; defer the rest
  behind issues).
- **Doctor additions** (§28): a "Build contract" row when a cached
  source exists (inspect the tree like the pipeline would, cheaply),
  and "CLI integration" row on Linux after Phase 3 (symlink +
  desktop file validation).

### E. Repair breadth & rollback UX (§25, §24)

- Repair currently only rebuilds when the bundle is broken. Add
  detection/fix for: marker/version mismatch, previous failed update
  in retention, broken symlink in the bundle, (Linux:) missing
  desktop entry/symlink — each action explicit + logged (spec §25).
- Document the rollback path: install-state already retains the
  previous bundle; surface it in failure UX ("Use installed version"
  option is the §17 unknown-contract case only) — keep CLI list per
  §30 stable; no standalone rollback command unless user request comes.

## Non-goals

- No new platforms (Windows/Linux phases carry those).
- No TUI restructure (Phase 1 covers it).
- No scheduled/unattended auto-update — spec never asks for it.

## Tests (suffix: all FakeRunner / httptest / temp-dir)

- Checker pass/fail fixtures; packer output tree snapshot for the §3
  naming; ldflags version report in `version` command test.
- CI workflow *linting*: a test that parses the workflow YAML for
  required jobs (keeps CI from rotting silently).
- Cache/cancellation property tests; §33 adversarial archive fixtures.
- Channel selection matrix tests (stable default, explicit dev
  requires git, bad channel → typed error).

## Definition of done

- A tagged release produces spec-§3 artifacts which the §38 checker
  accepts, published through CI; repeated downstream manual flow works
  on each supported platform binary (macOS now, others after their
  phases).
- Spec §44 checklist has no ❌ left for the platform row available at
  that time; every ✔/✘ answer has a test or page of code behind it.
- README status, DEVELOPMENT.md release section, AGENTS.md (if the
  no-deps rule wording needed one more nuance for packer helper),
  ARCHITECTURE.md updated.
