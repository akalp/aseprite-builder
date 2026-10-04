# AGENTS.md

Go 1.24 CLI (module `github.com/akalp/aseprite-builder`) that builds,
installs, updates and removes Aseprite from official source. Business and data
layers are stdlib-only; UI dependencies (Bubble Tea / Lip Gloss) are allowed
**only** inside `internal/tui` (spec §10, §36 — go.mod/go.sum are tracked
normally). Supported platforms today: macOS arm64/amd64, Windows 11 x64, and
Linux amd64 on Debian/Ubuntu, Fedora/RHEL, Arch, openSUSE and Void
(`internal/platform/{darwin,windows,linux}`, `internal/packages`; running with
no arguments opens the TUI). Docs:
`docs/PROJECT_SPEC.md` (the spec — treat as binding), `docs/ARCHITECTURE.md`,
`docs/DEVELOPMENT.md`.

## Commands

- `make check` — the verification gate: gofmt check + `go vet ./...` + `go test ./...`
- Formatting is plain `gofmt` (`make fmt` to write). No goimports/golangci config.
- Single package: `go test ./internal/app`; single test: `go test ./internal/app -run <Name>`.
- Cross-check the other platforms compile (their tagged tests included):
  `GOOS=windows GOARCH=amd64 go vet ./...` and `GOOS=linux GOARCH=amd64 go vet ./...`.
  To *execute* the linux-tagged tests on a macOS host, run them in a Linux
  container: `docker run --rm -v "$PWD":/src -w /src golang:1.24 go test ./...`.
- `make build` produces `./aseprite-builder` at the repo root (gitignored; `make clean`
  removes it). Note: `docs/DEVELOPMENT.md` claims the binary lands in `dist/` — the
  Makefile is the truth.
- Run the CLI in isolation — it writes to `$HOME/Library/Caches/aseprite-builder` and
  `$HOME/Library/Application Support/aseprite-builder`:
  `HOME=$(mktemp -d) go run ./cmd/aseprite-builder doctor`

## Non-negotiable rules

- **No version matrices.** Never hardcode Aseprite/CMake/Ninja/Skia versions;
  everything is discovered from upstream at runtime (spec §2). A new upstream
  release must never require a builder release. The one sanctioned exception:
  distribution package *names* (never versions) in `internal/packages/*`, which
  are platform knowledge.
- **Zero-vendored-toolchain** (spec §3): nothing third-party vendored, embedded,
  or downloaded at build time. Downloads land in user caches via `internal/paths`,
  never next to the repo — don't commit archives, extracted sources, toolchains,
  Skia packages, or built Aseprite artifacts.
- **No `runtime.GOOS` outside the platform layer** — allowed only in
  `internal/platformselector`, `internal/paths`, and `internal/platform`.
- **Never elevate silently** (spec §16/§33): `sudo` runs only after explicit
  user consent on a live TTY, visibly and with the terminal attached; `--yes`
  skips builder prompts only and never approves package installation. Platform
  packages stay distribution-managed — no `.deb`/`.rpm`, no repositories.
- **`internal/cli` never contains business logic** — only flags, output, and
  typed-error mapping. Business services live in `internal/app`; user-facing
  message mapping lives in `app.FriendlyMessage` so CLI and TUI cannot drift.
- **UI dependencies live only in `internal/tui`.** No package outside
  `internal/tui` may import Bubble Tea / Lip Gloss (guarded by a test in
  `internal/archtest`); `internal/app` must gain zero UI imports.
- Dependency direction: `cmd → internal/{cli,tui} → internal/app →
  internal/{aseprite,platform} → infra (runner, download, archive, upstream,
  paths, state, packages/{apt,dnf,pacman,zypper,xbps} + packages/selector)`.

## Testing rules

- Unit tests must not compile Aseprite or touch the network.
- Process behavior uses `runner.FakeRunner` (canned results, error injection,
  key/pattern matching); GitHub API shapes are faked with `httptest`; filesystem
  tests use real temp dirs (no in-memory fs shims).
- Pipeline tests materialize fake build outputs via `materializeBuildOutput`
  (see `internal/app/service_test.go`, `internal/aseprite/build_test.go`).

## Upstream contract changes

When the upstream *build contract* changes (not merely versions): adjust the
markers in `internal/aseprite/contract.go`, the orchestration in
`internal/aseprite/build.go` (+ `internal/aseprite/windowsbuild.go` for the
direct-CMake Windows flow), and bump the `contractLevel` const so install
state can tell generations apart. Unknown/mutated contracts must fail safely
(`UnknownContractError`), never be guessed.
