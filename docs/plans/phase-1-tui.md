# Phase 1 — TUI over the existing services (Spec Milestone 3)

**Status:** ✅ Complete
**Spec:** §10 (Technology), §12 (Normal User Experience), §34 (TUI Architecture), §39 (Failure UX), §7 (License notice)
**Depends on:** Milestones 1–2 ✅

## Goal

An interactive primary interface with the exact same capabilities as the
CLI, driven by the *already-existing* `internal/app` services. No
installer logic in UI models (literal spec sentence): the TUI translates
user choices into `Service` calls and renders `ProgressEvent` streams.

## Design decision (recorded)

Uses **Bubble Tea + Lip Gloss (+Bubbles where useful)** per spec §10.
This requires amending `AGENTS.md` at the start of this phase:

- The "stdlib only" rule narrows to: business/data layers remain
  stdlib-only and dependency-free; UI dependencies are allowed **only**
  inside `internal/tui`.
- `go.mod` / `go.sum` become tracked (spec §36 explicitly permits this).

## Package layout

```text
internal/tui/
    app.go        Bubble Tea program bootstrap, input wiring
    model.go      state machine: Model, Update, View
    screens.go    one component per logical screen
    events.go     app.ProgressEvent → view message plumbing
```

Dependency direction after this phase (update ARCHITECTURE.md):

```text
internal/cli ─┐
              ├─→ internal/app → internal/{aseprite,platform} → infra
internal/tui ─┘
```

`internal/app` must gain **zero Bubble Tea imports** — guard this with a
test that walks packages under `internal/` and fails if any file outside
`internal/tui` imports a UI library (cheap AST/grep test, no external
tooling).

## State machine

States per spec §34:

```text
welcome → home → systemcheck → installoptions → installing → installed
                → diagnostics → repair → settings → advanced
                → confirmation → error  (any →)
```

- `welcome`: first-run license/notice screen (§7): unofficial, Aseprite
  is a separate project, compiled locally, no binary redistributed,
  user is responsible for license compliance. Non-blocking (once per
  machine or until acknowledged; stored in config).
- `home`: mirrors the §12 mock — installed state (`CurrentInstall`) +
  latest stable (`LatestStable`) + menu: Install / Advanced / Settings /
  Doctor / Exit.
- `installing`: consumes `app.ProgressEvent` (already emits `Stage`,
  `Message`, `Current`/`Total` and build phases configure/compile) from
  a `tea.Cmd` running the pipeline on a goroutine; Ctrl+C cancels the
  pipeline context (services already honor ctx; downloads and the
  runner are cancellable).
- `error`: spec §39 screens. Failed install/update/rebuild print the
  typed-error friendly message (shared logic with `internal/cli` —
  extract the message mapping into `app` or a small shared layer so CLI
  and TUI cannot drift), plus actions: View log / Diagnostics / Retry /
  Back. Unknown-contract failures must be visually distinct.
- `diagnostics`: renders `DoctorReport` (reuse the same rows the CLI
  prints; `doctor` and the TUI screen must show identical *facts*).
- `settings`: backs onto `state.Config` (`Store.LoadConfig` /
  `SaveConfig` already exist and are currently unused) — channel,
  keep-workspace, verbose. Needs a tiny `app.Settings` accessor service
  so the TUI doesn't touch `state.Store` directly.
- `advanced`: pinned `--version` equivalent, keep workspace, clean
  rebuild, verbose logs. Dev-channel entries are **absent** until Phase
  5 ships the backend (no dead menu items).

## Services changes (minimal, concrete)

1. Extract `friendlyMessage(err)`-style typed-error → user-text mapping
   from `internal/cli/cli.go` into an app-level location shared by both
   frontends (no logic change in CLI behavior; tests follow it).
2. `app.Options` gains a `Config` accessor pair (load/save returning
   validated `state.Config`) used by Settings.
3. Wire persisted `state.Config` into `runPipeline` inputs
   (`KeepWorkspace`, verbose logging) so Settings actually work — today
   the CLI never loads config.json.

No pipeline/logic changes otherwise. The TUI must not duplicate
release resolution, tool resolution, contract inspection, or install
steps.

## Non-goals

- No new platform code, no build-system code, no download code.
- No TUI-only flags/states that the CLI cannot reach, and vice versa.
- No mouse protocol work; keyboard-only is fine for v1.
- Do not render raw compiler output as primary error text (§39/§40);
  log file path + last N lines behind "View details".

## Tests

- Pure model tests: `Update/View` transitions with synthetic
  `ProgressEvent`s, typed errors, key input — no terminal, no TTY.
- Happy-path and failure-path scripts against a fake-backed service
  (reuse the fakes from `internal/app/service_test.go`).
- The "UI deps only in internal/tui" guard test.
- `make check` green; CLI behavior untouched (existing `internal/cli`
  tests must pass unchanged apart from the extracted message mapping).

## Definition of done

- `aseprite-builder` with no arguments opens the TUI; a first-time user
  can install, see the §12-style summary of system checks, watch
  structured stage/compile progress, launch, update, rebuild, repair,
  run diagnostics, adjust settings, and uninstall — all without typing
  build-system words.
- §44 TUI column of the workflow matrix (install/launch/update/repair/
  rebuild/doctor/uninstall) works on macOS; CLI and TUI share services.
- AGENTS.md amended (dependency rule + layer graph), ARCHITECTURE.md §
  added, README status updated, this file's status moved to ✅ in the
  README table.
