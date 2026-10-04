# Development

## Requirements

- Go 1.24+
- macOS is the primary development host. The macOS slice is
  implemented; Windows x64 and Linux x86_64 (Debian/Ubuntu, Fedora/RHEL,
  Arch, openSUSE, Void) are implemented and cross-checked from macOS
  (`GOOS=… go vet`, plus a Linux container for executing the
  linux-tagged tests).

## Common commands

```bash
go build ./...              # compile everything
go test ./...               # full unit test suite
go vet ./...                # static checks
gofmt -l .                  # formatting check (should print nothing)

# Cross-check the other platforms (builds, vets and compiles the
# windows/linux-tagged tests without executing them).
GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...
GOOS=linux GOARCH=amd64 go build ./... && GOOS=linux GOARCH=amd64 go vet ./...
GOOS=linux GOARCH=amd64 go test -c -o /dev/null ./internal/platform/linux  # compile linux tests

# Execute the linux-tagged tests on a macOS host (Docker):
docker run --rm -v "$PWD":/src -w /src golang:1.24 go test ./...

go run ./cmd/aseprite-builder help
go run ./cmd/aseprite-builder doctor --json
```

Running the binary with no arguments in a terminal opens the
interactive TUI (spec §30); all subcommands use the non-interactive
CLI. The TUI is only opened for real terminals — piped stdout prints
the usage table instead, so automation is unaffected.

## Testing the TUI

`internal/tui` tests are pure model tests: `Update`/`View` transitions
driven by synthetic `app.ProgressEvent`s, typed errors and key
messages — no terminal, no TTY. `internal/archtest` guards the layer
rules (UI imports only in `internal/tui`; frontends never import each
other).

## Running the CLI against an isolated environment

The builder writes to the user's cache and data directories. For
isolated experiments override `HOME`:

```bash
TMPHOME=$(mktemp -d)
HOME="$TMPHOME" go run ./cmd/aseprite-builder doctor
```

## Testing principles (per the project spec)

- Unit tests must not compile Aseprite; pipeline tests materialize
  fake build outputs (`materializeBuildOutput`) instead.
- Process behavior is tested with `runner.FakeRunner` (canned results,
  error injection, pattern matching).
- Upstream metadata is tested with `httptest` servers shaped like the
  GitHub API; network is never required.
- Filesystem behavior (archives, state, install staging) uses real
  temp directories — no in-memory fs shims that diverge from reality.
- The packages layer (`internal/packages`, `internal/packages/apt`) is
  host-neutral and runs everywhere; the linux-tagged platform tests
  (install staging/retention/rollback, symlink/desktop/icon lifecycle,
  consent gating) execute via the Docker command above or on a Linux
  host.

## Updating upstream knowledge

 deliberate act: the code intentionally contains **no Aseprite/CMake/
Ninja/Skia version tables**. When upstream changes its *build
contract* (not merely versions), adjust the recognized markers in
`internal/aseprite/contract.go` and the orchestration in
`internal/aseprite/build.go`, and bump the contract level constant so
install state can tell generations apart. Legitimate contract changes
that require a builder release are documented in `PROJECT_SPEC.md`
section 2.

## Repo hygiene

```bash
make check          # gofmt + vet + test
make build          # build ./aseprite-builder at the repo root (gitignored)
make release        # cross-compile + pack the four spec §3 artifacts into dist/
make release-check  # re-run the §38 release-safety checker over dist/
make clean          # remove the repo-root binary and dist/
```

The Go module now tracks dependencies normally (`go.mod`/`go.sum`,
spec §36): UI libraries (Bubble Tea, Lip Gloss) are allowed only in
`internal/tui`; business and data layers stay stdlib-only.

Never commit: downloaded archives, extracted sources, toolchains, Skia
packages, built Aseprite artifacts, or `.cache`. `.gitignore` covers
the usual suspects; `make release` packs only project-owned output and
fails the §38 release-safety check (`cmd/releasecheck`) if an artifact
ever contains anything else.

## Releases

`make release` cross-compiles the four spec §3 artifacts into `dist/`,
stamping the version from `VERSION` or the nearest git tag (falling
back to `0.1.0-dev`) through
`-ldflags "-X main.version=…"`, then runs the §38 checker. Pushing a
`vX.Y.Z` tag runs `.github/workflows/release.yml`, which builds each
artifact on its own OS runner (macOS also cross-builds the Intel
binary), re-runs the checker over the merged set, and attaches the
archives to the GitHub Release with the `gh` CLI.

Tracker state lives in `internal/state`; `aseprite-builder version`
reports the stamped builder version. CI covers format/vet/tests on
ubuntu/macos/windows plus a `doctor --json` smoke and the release
safety job.

### Branch protection (owner action)

Configure the repository so `main` requires the CI checks to pass
before merging: the three `test (…-latest)` jobs and the
`release safety (spec §38)` job from `.github/workflows/ci.yml`.
The workflow-lint tests in `internal/archtest` keep the required jobs
from silently disappearing.

## Real-host end-to-end verification (manual)

CI runs fast checks only (format/vet/tests, a `doctor --json` smoke and
the §38 release check) — it never compiles Aseprite. The full
compile-and-install path is verified manually before a tag, from the
built release artifact, on each supported platform:

1. Unpack the platform's artifact and first run it in an isolated
   `HOME`: `HOME=$(mktemp -d) ./aseprite-builder doctor --json` — it
   must not touch the real profile, and the JSON must parse.
2. With the real profile: `./aseprite-builder doctor` — requirements
   and tool resolution look right; missing system packages come with
   one copy-paste command.
3. `./aseprite-builder install` — compiles from the official source,
   verifies the executable, installs it. Note the version and path.
4. `./aseprite-builder launch` — the installed application opens.
5. `./aseprite-builder update` — unless already up to date; the
   previous installation is retained and the new one activates.
6. `./aseprite-builder repair` — reports the installation healthy and
   rebuilds nothing.
7. `./aseprite-builder rebuild` — recompiles the installed version.
8. `./aseprite-builder uninstall` — removes only builder-owned files;
   the application path is gone and user files are intact.
9. Linux additionally: after install, check the
   `~/.local/bin/aseprite` symlink, the desktop entry and the app-menu
   icon; delete the symlink and run `repair` to watch it recreated.
10. Dev channel (once a cycle, on a macOS/Linux host with Git):
    `./aseprite-builder install --channel dev --version <sha>` checks
    out the explicit commit and builds it; `update` refuses with clear
    guidance; `uninstall` still cleans up.

Run the matrix before tagging: macOS arm64 and amd64, Windows 11 x64,
and Linux amd64 on at least Debian/Ubuntu plus one of
Fedora/Arch/openSUSE/Void. Record the completed runs in the release
notes.
