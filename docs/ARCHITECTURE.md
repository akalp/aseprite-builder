# Architecture

This document maps the source layout to the concepts in
[`PROJECT_SPEC.md`](PROJECT_SPEC.md). Read the spec first; this file
explains how the code implements it.

## Dependency direction

```text
cmd/aseprite-builder        (main: wire IO + signals)
        ↓
internal/cli                (command parsing, human output)
        ↓
internal/app                (application services: install/update/…)
        ↓                          ↓
internal/aseprite           internal/platform (+ platformselector)
(downstream domain)         (OS implementations)
        ↓
internal/dependencies, internal/aseprite build orchestration
        ↓
infrastructure: runner, download, archive, upstream, paths, state, logging, version
```

- **UI never contains business logic.** `internal/cli` only parses
  flags, prints output and maps typed errors. The future TUI (Bubble
  Tea) will consume the same `internal/app` services and
  `app.ProgressEvent`s.
- **No `runtime.GOOS` outside the platform layer**, except in exactly
  one selector: `internal/platformselector` (plus `internal/paths`,
  which is a platform in its own right and selects per-OS defaults for
  user directories).
- **No version matrices.** Versions come from upstream at runtime
  (see below).

## Runtime resolution (zero-vendored-toolchain)

Nothing third-party is vendored, embedded, or downloaded at build time
of aseprite-builder itself. Everything is resolved per-build:

| Requirement      | Discovery                                      | Reuse order                                     | Authoritative source |
|------------------|------------------------------------------------|--------------------------------------------------|----------------------|
| Aseprite source  | GitHub releases metadata (`internal/aseprite`) | already-extracted source tree for that version   | `github.com/aseprite/aseprite` stable release assets (`Aseprite-vX-Source.zip`), digest-verified when GitHub publishes one |
| CMake            | min version parsed from the source's `CMakeLists.txt` | system `cmake` → cached dist → download | `github.com/Kitware/CMake` macOS universal tarball |
| Ninja            | presence of any working version                | system `ninja` → cached dist → download          | `github.com/ninja-build/ninja` `ninja-mac.zip` |
| Skia             | `laf/misc/skia-tag.txt` + `skia-url.sh` *executed from the extracted source* | cached verified extraction | the exact URL the upstream script produced |
| Xcode/SDK/clang  | probe commands (`internal/platform/darwin`)     | system-managed only — never installed by us      | Apple (not downloaded) |

Downloads are verified against upstream digests when they exist
(Aseprite source: yes, via GitHub asset digests; aseprite/skia assets
currently publish none, which is recorded in extraction metadata).
All downloads land in user-local cache directories (`internal/paths`),
never next to the binary and never globally installed.

## The upstream build contract

`internal/aseprite/contract.go` inspects the extracted source tree and
only proceeds when every marker of the *known contract* is present
(entry points `build.sh`, Skia machinery under `laf/misc/`, the
`project(aseprite)` CMake declaration, the macOS bundle output rule in
`src/CMakeLists.txt`, and the complete-submodule signals upstream's own
CMake checks for). Unknown or mutated contracts fail before anything is
built — `aseprite.UnknownContractError`.

The build itself is *orchestrated, not reimplemented*:
`aseprite.Builder` prepares upstream's own `build.sh --auto` config
files (`<source>/.build/userkind`, `builds_dir`, `*_skia_dir`), links
the resolved Skia into the location upstream looks at, prepends
resolved tool PATH entries, and runs `bash build.sh --auto --norun`.
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

## State and logs

- `~/Library/Caches/aseprite-builder/` — downloads, extracted sources,
  toolchains, Skia, logs (per-operation files with full command
  records).
- `~/Library/Application Support/aseprite-builder/` — `config.json`,
  `install.json` (the install record: version, tag, source URL,
  digest, Skia tag, contract fingerprint, install path, retained
  previous path, workspace path).

Log redaction is enforced in `internal/logging`: environment keys
matching credential-shaped names are recorded as `(redacted)`, and raw
environment values are never dumped.

## Testing

Unit tests use fake runners (`runner.FakeRunner` with exact-key and
pattern matching), fake release providers, a fake platform, and a
fetch hook; no Aseprite compilation happens in tests. See
`DEVELOPMENT.md` for commands.
