# Implementation Plan — Phases

This folder turns the milestones in [`../PROJECT_SPEC.md`](../PROJECT_SPEC.md)
(§42) into concrete, reviewable phases for what remains to be built. The
spec is binding; these files only sequence and detail the work. Each
phase is intended to be one work branch (or small series of branches)
ending in a green `make check`.

Read [`../ARCHITECTURE.md`](../ARCHITECTURE.md) for how the implemented
code maps to the spec, and [`../../AGENTS.md`](../../AGENTS.md) for the
invariants every phase must preserve.

## Status snapshot (audited 2026-10-04, Phase 1 complete)

`make check` passes (gofmt + go vet + go test, no network, no Aseprite
compile in tests).

| Spec milestone | Status | Evidence in code |
|---|---|---|
| 1 — Platform-independent core | ✅ Complete | `internal/{paths,state,logging,runner,download,archive,upstream,version,aseprite,dependencies}`, CLI over `internal/app` services |
| 2 — macOS arm64 vertical slice | ✅ Complete | `internal/platform/darwin` (detect / requirements / transactional install + retention rollback / launch / uninstall), contract inspection (`internal/aseprite/contract.go`), build orchestration via upstream `build.sh --auto --norun`, Skia discovery from the source tree, update / rebuild / repair / uninstall / doctor `--json`, CLI HUD, Libre-license notice |
| 3 — TUI | ✅ Complete | `internal/tui` (Bubble Tea + Lip Gloss): welcome (§7 notice) → home (§12 mock) → systemcheck → installoptions → installing (structured progress, Ctrl+C cancels) → result; diagnostics / settings (`state.Config` now read+written) / advanced (pinned install, rebuilds, uninstall with purge scopes); §39 error screens with shared `app.FriendlyMessage`, distinct §17 unknown-contract screen; no-args TTY invocation opens it |
| 4 — Windows x64 | ❌ Not started | only `darwin` implements `platform.Platform`; `SupportedTargets()` has `darwin` only |
| 5 — Linux Debian/Ubuntu | ❌ Not started | no packages layer |
| 6 — Additional Linux distros | ❌ Not started | — |
| 7 — Hardening | ◐ Partial | rollback/retention ✅, contract validation ✅, download verification ✅ (where upstream publishes digests), cancellation ✅ (signals wired) — CI, release packaging + §38 release-safety checks, dev channel, settings/advanced surface, security review ❌ |

Spec items deliberately **deferred** into the phases below:
development channel (§14 — the settings screen shows stable only
until its backend exists), release artifacts (§3/§38), CI matrix
(§37).

## Decisions

| Decision | Resolution |
|---|---|
| TUI library (spec §10 prefers Bubble Tea; AGENTS.md said "stdlib only") | **Bubble Tea + Lip Gloss** — user-approved 2026-10-03. AGENTS.md amended when Phase 1 started: the stdlib-only rule narrowed to *business layers*; UI deps live only in `internal/tui` and `go.mod`/`go.sum` are tracked normally (spec §36 already permits this). Bubbles was **not** taken: the only candidate need (pinned-version text entry) is hand-rolled in ~15 lines (spec rule 11). |

## Phases

| # | Phase | Spec reference | File | Status |
|---|---|---|---|---|
| 1 | TUI over the existing services | §10, §12, §34, §39 | [`phase-1-tui.md`](phase-1-tui.md) | ✅ Complete |
| 2 | Windows x64 platform | §11, §21, §37 | [`phase-2-windows.md`](phase-2-windows.md) | Planned |
| 3 | Linux — Debian/Ubuntu vertical slice | §11, §16, §22 | [`phase-3-linux-debian-ubuntu.md`](phase-3-linux-debian-ubuntu.md) | Planned |
| 4 | Linux — additional distributions | §11, §16, §22 | [`phase-4-linux-distros.md`](phase-4-linux-distros.md) | Planned |
| 5 | Release engineering & hardening | §3, §14, §24, §25, §30, §33, §37, §38, §41 | [`phase-5-hardening-release.md`](phase-5-hardening-release.md) | Planned |

## Ground rules for every phase

1. **The gate is `make check`** (gofmt + vet + tests). Tests never touch
   the network and never compile Aseprite; `runner.FakeRunner` and
   `httptest` fakes only; real temp dirs for filesystem tests.
2. **Zero-vendored-toolchain** is unchanged: nothing third-party is
   vendored, embedded, committed, or downloaded at *builder build* time.
   Runtime resolution (system → cache → authoritative download) is the
   only path to tools the user lacks.
3. **No version matrices.** Anything the upstream tree can tell us
   (CMake minimum, Skia tag, output paths) is discovered from it.
   Platform package *names* for system-managed compiler deps are the
   one exception and live in the platform layer, documented as such.
4. **Platform isolation holds.** New OS behavior goes through
   `platform.Platform` (+ new `internal/packages` for Linux); no
   `runtime.GOOS` outside `platformselector`, `paths`, `platform`.
5. **Fail safely on unknown upstream contracts** (§2, §17); bump
   `contractLevel` only when the recognized *contract shape* changes —
   never for upstream version churn.
6. **Transactional installs remain untouched-working-install guarantees**
   for update/rebuild (§23); no silent elevation (§16); uninstall only
   provably-owned files (§27).
7. **Docs move with the code**: `AGENTS.md` (layer rules, milestone text),
   `ARCHITECTURE.md` (new layers), `DEVELOPMENT.md`, `README.md` status
   block. A phase is not done while any of these contradict the tree.
8. Update phase status in this README's table when a phase completes.
