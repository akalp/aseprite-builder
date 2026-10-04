# Architecture

This document maps the source layout to the concepts in
[`PROJECT_SPEC.md`](PROJECT_SPEC.md). Read the spec first; this file
explains how the code implements it.

## Dependency direction

```text
cmd/aseprite-builder        (main: wire IO + signals; no-args → TUI)
        ↓
internal/cli    internal/tui            (frontends: parse/print / Bubble Tea)
        └──────────────┬────────────────┘
                       ↓
               internal/app            (application services: install/update/…)
                       ↓                          ↓
        internal/aseprite           internal/platform (+ platformselector)
        (downstream domain)         (OS implementations)
                       ↓
        internal/dependencies, internal/aseprite build orchestration
                       ↓
        infrastructure: runner, download, archive, upstream, paths, state,
                        logging, version, packages (apt)
```

- **UI never contains business logic.** `internal/cli` only parses
  flags, prints output and maps typed errors; `internal/tui` is a
  state machine that translates user choices into `internal/app`
  calls and renders `app.ProgressEvent` streams (spec §34). The two
  frontends are siblings and never import each other.
- **Shared user-facing facts live in `internal/app`:** typed-error →
  friendly-message mapping (`app.FriendlyMessage`) and the doctor
  table (`DoctorReport.DoctorRows`) are built once and rendered by
  both frontends, so CLI and TUI cannot drift.
- **No `runtime.GOOS` outside the platform layer**, except in exactly
  one selector: `internal/platformselector` (plus `internal/paths`,
  which is a platform in its own right and selects per-OS defaults for
  user directories).
- **No version matrices.** Versions come from upstream at runtime
  (see below).

## TUI (interactive primary interface)

`internal/tui` is the only package allowed to import UI dependencies
(Bubble Tea, Lip Gloss — spec §10/§36; guarded by `internal/archtest`):

```text
internal/tui/
    app.go        program bootstrap, TTY gating, post-exit summary
    model.go      state machine: Model, Update, menus, confirmations
    screens.go    one renderer per logical screen
    events.go     app.ProgressEvent → view messages, stage table, op goroutine
```

Screens per spec §34: welcome (§7 notice, acknowledged once per
machine, stored in `state.Config`) → home (§12 mock) → systemcheck
(preflight doctor facts) → installoptions → installing (structured
progress; Ctrl+C cancels the pipeline context) → result → diagnostics
/ settings / advanced / confirmation / error (§39 failure UX with
View log / Diagnostics / Retry / Back; unknown contracts get a
distinct screen with a different action set per §17).

UpdateAvailable and Updating collapse into the home banner and the
installing screen (update-labeled); Installed is the result screen.
Operations run on a goroutine whose context is canceled on Ctrl+C and
again at program exit — no pipeline survives a dead TUI.

## Runtime resolution (zero-vendored-toolchain)

Nothing third-party is vendored, embedded, or downloaded at build time
of aseprite-builder itself. Everything is resolved per-build:

| Requirement      | Discovery                                      | Reuse order                                     | Authoritative source |
|------------------|------------------------------------------------|--------------------------------------------------|----------------------|
| Aseprite source  | GitHub releases metadata (`internal/aseprite`) | already-extracted source tree for that version   | `github.com/aseprite/aseprite` stable release assets (`Aseprite-vX-Source.zip`), digest-verified when GitHub publishes one |
| CMake            | min version parsed from the source's `CMakeLists.txt` | system `cmake` → cached dist → download | `github.com/Kitware/CMake` macOS universal tarball / Windows x86_64 zip (per host) |
| Ninja            | presence of any working version                | system `ninja` → cached dist → download          | `github.com/ninja-build/ninja` `ninja-win.zip` (Windows) / `ninja-mac.zip` (macOS) |
| Skia             | `laf/misc/skia-tag.txt` + `skia-url.sh` *executed from the extracted source* (bash) — or, on Windows where bash is absent, *parsed* from the script's own URL/asset templates and validated against the aseprite/skia asset list | cached verified extraction | the exact URL upstream's script resolves/encodes (`Skia-<OS>-Release-<cpu>.zip`) |
| Xcode/SDK/clang  | probe commands (`internal/platform/darwin`)     | system-managed only — never installed by us      | Apple (not downloaded) |
| Visual Studio / Build Tools / MSVC environment / Windows SDK | `vswhere.exe` (upstream build.cmd's discovery: `Microsoft.VisualStudio.Component.VC.Tools.x86.x64` → `VC\Auxiliary\Build\vcvars64.bat`) + Windows Kits include probe; `vcvars64.bat && set` captures the build environment | system-managed only — never installed by us | Microsoft (not downloaded) |
| Linux build packages (g++, clang, cmake, ninja-build, X11/GL/fontconfig dev libs) | `internal/packages/apt` reads `/etc/os-release` (ID/ID_LIKE) and probes the dpkg status database (`dpkg-query`); required package *names* are documented in the apt package (names only, never versions) | system-managed only: installed by the user (or, with explicit TTY consent, by the visible `sudo apt-get install …` the CLI runs) — never bundled, never elevated silently | the distribution's own repositories (spec §16) |

Downloads are verified against upstream digests when they exist
(Aseprite source: yes, via GitHub asset digests; aseprite/skia assets
currently publish none, which is recorded in extraction metadata).
All downloads land in user-local cache directories (`internal/paths`),
never next to the binary and never globally installed.

## The upstream build contract

`internal/aseprite/contract.go` inspects the extracted source tree and
only proceeds when every marker of the *known contract* is present.
Per platform:

- **macOS** — entry point `build.sh`, Skia machinery under `laf/misc/`,
  the `project(aseprite)` CMake declaration, the Aseprite.app output
  rule in `src/CMakeLists.txt`, and the complete-submodule signals
  upstream's own CMake checks for.
- **Windows** — the same structural markers plus upstream's Windows
  entry points (`build.cmd`, `build.ps1`) and the direct exe + `bin/data`
  output rules in `src/CMakeLists.txt`. Contracts carry an OS token in
  their fingerprint so state can tell platforms/generations apart.
- **Linux** — the base structural markers (`build.sh`, the Skia
  machinery, `project(aseprite)`) plus the plain `add_executable(aseprite)`
  + `bin/data` output rules — the same non-bundle shape as Windows,
  but consumed through upstream's `build.sh` instead of the direct-CMake
  Windows flow. No Windows entry points or macOS plist are required;
  the fingerprint carries `os=linux`.

Unknown or mutated contracts fail before anything is built —
`aseprite.UnknownContractError`.

The build itself is *orchestrated, not reimplemented*:

- **macOS/Linux**: `aseprite.Builder` prepares upstream's own
  `build.sh --auto` config files (`<source>/.build/userkind`,
  `builds_dir`, `*_skia_dir`), links the resolved Skia into the location
  upstream looks at, prepends resolved tool PATH entries, and runs
  `bash build.sh --auto --norun`.
- **Windows** (no bash on stock Windows — spec §9): the builder runs
  the *same two cmake commands* build.sh runs for users
  (`cmake -B <src>/build -S <src> -G Ninja -DLAF_BACKEND=skia
  -DSKIA_DIR=… -DSKIA_LIBRARY_DIR=…`, then
  `cmake --build <src>/build -- aseprite`) inside the Visual Studio
  environment the platform captured from `vcvars64.bat`
  (`internal/aseprite/windowsbuild.go`). No Git Bash dependency.

If output locations shift upstream, the builder detects the mismatch
(`ContractViolationError`) instead of guessing.

## Update/rollback safety

`internal/platform/darwin` installs by staging a copy of the produced
`Aseprite.app` under `~/Applications/.aseprite-builder/staging`,
ad-hoc signing it, verifying it (`--version` check), retaining the
current bundle in `…/previous/`, and only then renaming the staged
bundle into `~/Applications/Aseprite.app`. Any failure before
activation rolls back the rename. An existing bundle that lacks the
builder's ownership marker is never replaced unless the user passes
`--force` (this also applies when the user installed an official
Aseprite there). The previous version is retained for one generation.

`internal/platform/windows` mirrors this with a directory layout:
the build's `bin` payload is staged under
`%LOCALAPPDATA%\Programs\.aseprite-builder\staging\Aseprite`, verified
(`aseprite.exe --version`), the current directory is retained in
`…\previous\`, and the staged directory is renamed into
`%LOCALAPPDATA%\Programs\Aseprite`. The ownership marker lives next to
the executable; a Start-Menu `.lnk` (minimal stdlib Shell Link writer)
is created on install and removed on uninstall. No elevation, no
registry entries.

`internal/platform/linux` uses the same directory transaction under
`~/.local/opt/aseprite` (marker next to the executable,
`.aseprite-builder/{staging,previous}` retention, rename activation with
rollback). After activation it refreshes the user-local integration
best-effort and never clobbers foreign files: `~/.local/bin/aseprite`
(a symlink only when it points into the builder's own install root),
`~/.local/share/applications/aseprite.desktop` (upstream's own
`src/desktop/linux/aseprite.desktop` is the template when the source
ships one; a marker comment proves ownership on update/uninstall) and
the hicolor icon theme (`data/icons`, sizes read from the PNG headers).
`update-desktop-database`/`gtk-update-icon-cache` run best-effort.
`launch` starts the installed executable detached (no terminal emulator
assumed). Missing distribution packages are reported with the exact
`sudo apt-get install …` command; the CLI runs it visibly only after
explicit TTY consent (the TUI is instruction-only) — never silently,
never from `--yes`.

## State and logs

- `~/Library/Caches/aseprite-builder/` — downloads, extracted sources,
  toolchains, Skia, logs (per-operation files with full command
  records). Windows: `%LOCALAPPDATA%\aseprite-builder\…`; Linux:
  `$XDG_CACHE_HOME/aseprite-builder/…` (or `~/.cache/aseprite-builder/…`).
- `~/Library/Application Support/aseprite-builder/` — `config.json`,
  `install.json` (the install record: version, tag, source URL,
  digest, Skia tag, contract fingerprint, install path, retained
  previous path, workspace path). Windows: `%LOCALAPPDATA%\
  aseprite-builder\…`; Linux: `$XDG_DATA_HOME/aseprite-builder/…`
  (or `~/.local/share/aseprite-builder/…`).

Log redaction is enforced in `internal/logging`: environment keys
matching credential-shaped names are recorded as `(redacted)`, and raw
environment values are never dumped.

## Testing

Unit tests use fake runners (`runner.FakeRunner` with exact-key and
pattern matching), fake release providers, a fake platform, and a
fetch hook; no Aseprite compilation happens in tests. The packages
layer (`internal/packages`, `internal/packages/apt`) is host-neutral,
so its probes/commands are tested on every host; the linux-tagged
platform suite compiles under `GOOS=linux go vet` and executes in a
Linux container (or on a Linux host). See `DEVELOPMENT.md` for
commands.
