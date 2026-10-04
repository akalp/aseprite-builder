# Development

## Requirements

- Go 1.24+
- macOS is the primary development host (the first vertical slice is
  macOS arm64/amd64; Windows x64 is implemented and cross-verified
  from macOS via the Go cross-build).

## Common commands

```bash
go build ./...              # compile everything
go test ./...               # full unit test suite
go vet ./...                # static checks
gofmt -l .                  # formatting check (should print nothing)

# Windows (cross-check from macOS/ci): builds, vets and compiles the
# windows-tagged tests without executing them.
GOOS=windows GOARCH=amd64 go build ./...
GOOS=windows GOARCH=amd64 go vet ./...

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
make clean          # remove the repo-root binary and dist/
```

The Go module now tracks dependencies normally (`go.mod`/`go.sum`,
spec §36): UI libraries (Bubble Tea, Lip Gloss) are allowed only in
`internal/tui`; business and data layers stay stdlib-only.

Never commit: downloaded archives, extracted sources, toolchains, Skia
packages, built Aseprite artifacts, or `.cache`. `.gitignore` covers
the usual suspects; release packaging scripts (a later milestone) must
run the release safety checks from spec section 38.
