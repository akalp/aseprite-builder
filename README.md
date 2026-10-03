# aseprite-builder

`aseprite-builder` is an **unofficial** third-party tool that builds,
installs, updates, repairs and removes the open-source [Aseprite]
sprite editor from its official source code — without requiring you to
know anything about Git, CMake, Ninja, Skia, compilers or build
directories.

> Not affiliated with or endorsed by Aseprite or Igara Studio.
> Aseprite is compiled locally from official source; this tool
> **never redistributes** compiled Aseprite binaries.

## Current status

The project is at the first vertical slice:

- **macOS Apple Silicon + Intel** (arm64/amd64), CLI only.
- Stable-channel Aseprite resolution from official GitHub release
  metadata (no Git required).
- Runtime-resolved dependencies: system CMake/Ninja are reused when
  compatible; missing tools are downloaded from their authoritative
  upstream sources into the user-local cache. The required Skia
  revision is **discovered from the Aseprite source tree itself**.
- Transactional installs: a failing build or verification never
  touches an existing working installation; the previous version is
  retained for one generation.

Windows and Linux support and the interactive TUI are planned
milestones (see `docs/PROJECT_SPEC.md`).

## Quick start (macOS)

```bash
# Build it
go build -o aseprite-builder ./cmd/aseprite-builder

# Diagnose your machine
./aseprite-builder doctor

# Install the latest stable Aseprite (compiles locally)
./aseprite-builder install

# Launch / update / remove
./aseprite-builder launch
./aseprite-builder update
./aseprite-builder uninstall
```

## What install does

```text
detect macOS + architecture
        ↓
verify system-managed Apple build requirements (Xcode CLT, SDK)
        ↓
resolve the latest stable Aseprite source release
        ↓
download + verify + extract the official source archive
        ↓
inspect the upstream build contract (fail safely if unknown)
        ↓
detect compatible CMake/Ninja already on this machine
        ↓
runtime-download missing tools into the user cache when required
        ↓
discover the required Skia revision from upstream data
        ↓
run the upstream build flow (build.sh --auto)
        ↓
verify the resulting executable
        ↓
install a native ~/Applications/Aseprite.app
```

## Documentation

- [`docs/PROJECT_SPEC.md`](docs/PROJECT_SPEC.md) — the full product spec
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — how the code maps to it
- [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) — development workflow

[Aseprite]: https://www.aseprite.org/
