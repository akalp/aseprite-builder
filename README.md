# aseprite-builder

`aseprite-builder` is an **unofficial** third-party tool that builds,
installs, updates, repairs and removes the open-source [Aseprite]
sprite editor from its official source code — without requiring you to
know anything about Git, CMake, Ninja, Skia, compilers or build
directories.

> Not affiliated with or endorsed by Aseprite or Igara Studio.
> Aseprite is compiled locally from official source; this tool
> **never redistributes** compiled Aseprite binaries. aseprite-builder
> is MIT-licensed; Aseprite itself is governed by its own EULA — see
> [`NOTICE`](NOTICE).

## Releases

Tagged releases are built and published by CI from the tag's commit:
each artifact is compiled under Go on its own platform's runner, the
spec §38 release-safety checker verifies that every archive contains
only the `aseprite-builder` executable, and the four archives are
attached to the [GitHub Release][releases]:

```text
aseprite-builder_Darwin_arm64.tar.gz   (macOS Apple Silicon)
aseprite-builder_Darwin_x86_64.tar.gz  (macOS Intel)
aseprite-builder_Windows_x86_64.zip    (Windows 11 x64)
aseprite-builder_Linux_x86_64.tar.gz   (Linux x86_64)
```

Download the archive for your platform, unpack the single executable,
and run it — no installer, no third-party runtime, no build tools
needed just to start.

Maintainers: `make release` cross-compiles and packs the same four
artifacts into `dist/` locally and runs the §38 checker;
`go run ./cmd/releasecheck dist/*` re-runs the check alone. Pushing a
`vX.Y.Z` tag runs the authoritative CI build, which stamps the version
from the tag.

[releases]: https://github.com/akalp/aseprite-builder/releases

## Current status

The project is at the first vertical slice plus its interactive UI, and
supports Aseprite's major desktop build platforms:

- **macOS Apple Silicon + Intel** (arm64/amd64), CLI **and TUI**.
- **Windows 11 x64** (amd64): Microsoft toolchain detected
  (vswhere/vcvars64), Aseprite compiled directly with CMake + Ninja
  (no bash, no Git Bash), user-local install to
  `%LOCALAPPDATA%\Programs\Aseprite`, Start-Menu shortcut, same
  transactional updating/uninstall semantics. CLI end-to-end; the TUI
  inherits the same services.
- **Linux x86_64** (amd64): distribution detected from `/etc/os-release`
  with Debian/Ubuntu (`apt`), Fedora/RHEL (`dnf`), Arch (`pacman`),
  openSUSE (`zypper`) and Void (`xbps`) package backends. Missing build
  packages are reported with the exact distribution command
  (`sudo apt-get install …`, `sudo dnf install …`, …), run visibly only
  after your explicit consent — never silently. Aseprite is compiled
  through upstream's `build.sh`, installed user-locally to
  `~/.local/opt/aseprite`, with a `~/.local/bin/aseprite` CLI symlink
  plus a desktop entry and hicolor icons so Aseprite shows up in the
  app menu. Same transactional updating/uninstall semantics.
- **Running `aseprite-builder` with no arguments opens the interactive
  TUI** (install / launch / update / repair / rebuild / diagnostics /
  settings / uninstall — same services as the CLI, structured progress,
  license notice on first run). The non-interactive CLI remains for
  advanced users and automation.
- Stable releases resolve from official GitHub release metadata (no
  Git required). An advanced **development channel** can build an
  explicit commit (`install --channel dev --version <sha>`, requires
  Git); stable stays the default everywhere.
- Advanced preferences stay out of the default flow: custom install
  directory, source retention, system-vs-cached CMake/Ninja preference,
  previous-build retention, keep-workspace, verbose logs — all in the
  TUI Settings screen and as per-run flags.
- Runtime-resolved dependencies: system CMake/Ninja are reused when
  compatible; missing tools are downloaded from their authoritative
  upstream sources into the user-local cache. The required Skia
  revision is **discovered from the Aseprite source tree itself**.
- Transactional installs: a failing build or verification never
  touches an existing working installation; the previous version is
  retained for one generation.

## Quick start

```bash
# Build it
go build -o aseprite-builder ./cmd/aseprite-builder

# Open the interactive UI (install / update / repair / doctor / …)
./aseprite-builder

# …or use the non-interactive CLI
./aseprite-builder doctor
./aseprite-builder install
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

On Linux the same pipeline runs with distribution-native package
handling: `/etc/os-release` identifies the distribution (Debian/Ubuntu,
Fedora/RHEL, Arch, openSUSE and Void are supported), missing build
packages are shown as a single `sudo apt-get install …` /
`sudo dnf install …` / `sudo pacman -S --needed …` /
`sudo zypper install …` / `sudo xbps-install -S …` command, and the
result is installed to `~/.local/opt/aseprite` with a
`~/.local/bin/aseprite` symlink, a desktop entry and hicolor icons. If
you consent on a live terminal the CLI runs that install command
visibly for you; otherwise it stops and prints the exact command to run
yourself.

## Documentation

- [`docs/PROJECT_SPEC.md`](docs/PROJECT_SPEC.md) — the full product spec
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — how the code maps to it
- [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) — development workflow
- [`NOTICE`](NOTICE) — licensing: builder MIT, Aseprite EULA

[Aseprite]: https://www.aseprite.org/
