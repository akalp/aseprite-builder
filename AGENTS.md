# AGENTS.md

Go 1.24 CLI (module `github.com/aseprite-builder/aseprite-builder`) that builds,
installs, updates and removes Aseprite from official source. Stdlib only —
no go.sum, no third-party deps. First vertical slice is macOS arm64/amd64 only
(other platforms fail fast in `internal/cli`). Docs: `docs/PROJECT_SPEC.md` (the
spec — treat as binding), `docs/ARCHITECTURE.md`, `docs/DEVELOPMENT.md`.

## Commands

- `make check` — the verification gate: gofmt check + `go vet ./...` + `go test ./...`
- Formatting is plain `gofmt` (`make fmt` to write). No goimports/golangci config.
- Single package: `go test ./internal/app`; single test: `go test ./internal/app -run <Name>`.
- `make build` produces `./aseprite-builder` at the repo root (gitignored; `make clean`
  removes it). Note: `docs/DEVELOPMENT.md` claims the binary lands in `dist/` — the
  Makefile is the truth.
- Run the CLI in isolation — it writes to `$HOME/Library/Caches/aseprite-builder` and
  `$HOME/Library/Application Support/aseprite-builder`:
  `HOME=$(mktemp -d) go run ./cmd/aseprite-builder doctor`

## Non-negotiable rules

- **No version matrices.** Never hardcode Aseprite/CMake/Ninja/Skia versions;
  everything is discovered from upstream at runtime (spec §2). A new upstream
  release must never require a builder release.
- **Zero-vendored-toolchain** (spec §3): nothing third-party vendored, embedded,
  or downloaded at build time. Downloads land in user caches via `internal/paths`,
  never next to the repo — don't commit archives, extracted sources, toolchains,
  Skia packages, or built Aseprite artifacts.
- **No `runtime.GOOS` outside the platform layer** — allowed only in
  `internal/platformselector`, `internal/paths`, and `internal/platform`.
- **`internal/cli` never contains business logic** — only flags, output, and
  typed-error mapping. Business services live in `internal/app`.
- Dependency direction: `cmd → internal/cli → internal/app →
  internal/{aseprite,platform} → infra (runner, download, archive, upstream,
  paths, state)`.

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
`internal/aseprite/build.go`, and bump the `contractLevel` const so install
state can tell generations apart. Unknown/mutated contracts must fail safely
(`UnknownContractError`), never be guessed.
