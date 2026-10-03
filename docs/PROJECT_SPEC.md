# Aseprite Source Builder

## Project Specification

**Status:** Initial implementation specification  
**Working name:** `aseprite-builder`

> `aseprite-builder` is an unofficial third-party tool. It is not affiliated with or endorsed by Aseprite or Igara Studio.

---

## 1. Product Goal

`aseprite-builder` is a cross-platform terminal application that lets ordinary users build, install, update, repair, launch, and uninstall Aseprite from its official source code without manually dealing with Git, CMake, Ninja, Skia, compiler flags, build directories, or platform-specific build commands.

The primary interface is an interactive TUI.

The same application must also expose a non-interactive CLI for advanced users, automation, testing, and diagnostics.

The intended user experience is:

```text
Download aseprite-builder
        ↓
Run it
        ↓
Choose "Install Aseprite"
        ↓
System requirements are checked
        ↓
Official Aseprite source is resolved
        ↓
Required runtime dependencies are discovered
        ↓
Existing compatible tools are reused
        ↓
Missing downloadable tools are fetched from authoritative sources
        ↓
Aseprite is compiled locally
        ↓
A native application is installed
        ↓
Launch Aseprite
```

A normal user should not need to know what CMake, Ninja, Skia, Git, Xcode, Visual Studio, SDKs, or compiler flags are.

---

## 2. Core Architectural Principle

The builder should be as close as possible to a **one-time implementation**.

A new release of Aseprite, CMake, Ninja, Skia, or another upstream dependency must **not** automatically require a new release of `aseprite-builder`.

The builder should require a new release only when an upstream integration contract changes in a way that the existing builder can no longer safely understand or orchestrate.

The key rule is:

> **Aseprite Builder should require a new release only when the upstream build contract changes, not when upstream application or dependency versions change.**

And:

> **Never encode release-specific dependency matrices when the same information can be discovered from upstream metadata or tooling at runtime.**

Examples of changes that should **not** require a new builder release:

```text
Aseprite 1.x.a → 1.x.b
CMake 4.x → 4.y
Ninja 1.x → 1.y
Skia revision changes
new stable Aseprite release
new upstream source archive
```

Examples that **may** require a new builder release:

```text
Aseprite removes or fundamentally changes its build scripts
Aseprite changes the structure used to declare Skia requirements
GitHub release/source distribution semantics change incompatibly
Aseprite adopts a completely different build system
required platform toolchain contracts change incompatibly
expected build output layout changes incompatibly
```

When such an incompatibility is detected, the builder must fail safely rather than guessing.

---

## 3. Zero-Vendored-Toolchain Principle

`aseprite-builder` must ship only its own executable and project-owned metadata required for the builder itself.

It must **not** vendor, embed, commit, or ship third-party build tools, installers, SDKs, source archives, prebuilt Aseprite binaries, or Skia packages.

The release artifacts should look approximately like:

```text
aseprite-builder_Darwin_arm64.tar.gz
└── aseprite-builder

aseprite-builder_Darwin_x86_64.tar.gz
└── aseprite-builder

aseprite-builder_Windows_x86_64.zip
└── aseprite-builder.exe

aseprite-builder_Linux_x86_64.tar.gz
└── aseprite-builder
```

The following must **not** be included in repository release artifacts:

```text
cmake
ninja
skia
git
Aseprite source
Aseprite binaries
Visual Studio installers
Xcode installers
Windows SDK installers
Linux .deb/.rpm packages
compiler toolchains
third-party setup executables
downloaded archives
runtime caches
```

The repository must remain source-oriented.

Generated binaries, downloaded dependencies, temporary toolchains, source archives, build output, and caches must not be committed.

---

## 4. Runtime-Resolved Dependency Model

Dependencies are resolved at runtime.

The preferred strategy is always:

```text
detect existing compatible tool
        ↓
compatible?
  yes ───────→ reuse it
  no
  ↓
resolve compatible upstream artifact
  ↓
download from authoritative source
  ↓
verify
  ↓
store in local cache
  ↓
use without globally installing it
```

This applies where technically reasonable to tools such as:

```text
CMake
Ninja
Skia artifacts
other redistributable build helpers
```

The application should prefer an already-installed compatible system tool before downloading its own cached copy.

The builder must not require Homebrew, Chocolatey, Scoop, npm, Python package managers, or other third-party package managers simply to run.

---

## 5. Do Not Hard-Code Tool Versions Without Necessity

Avoid patterns such as:

```go
const cmakeVersion = "4.1.2"
const ninjaVersion = "1.13.1"
const skiaVersion = "m124"
```

unless a pinned version is required by a stable upstream contract and cannot be discovered dynamically.

Prefer:

```text
Aseprite source
      ↓
inspect upstream build metadata / scripts
      ↓
discover requirement
      ↓
check existing compatible tool
      ↓
resolve current compatible artifact if needed
```

The builder should derive Aseprite-specific requirements from upstream source whenever possible.

In particular, the builder should not maintain its own Aseprite-version-to-Skia-version matrix if the required information can be obtained from the Aseprite source tree or official build tooling.

---

## 6. Upstream Build Orchestration

The builder must orchestrate upstream Aseprite build logic rather than reimplementing it.

Preferred model:

```text
builder
  ↓
official Aseprite source
  ↓
official build scripts / build metadata
  ↓
builder prepares environment
  ↓
builder invokes upstream build flow
  ↓
builder verifies result
```

The builder should avoid duplicating large amounts of upstream build configuration.

Its responsibility is to:

```text
detect environment
resolve source
resolve dependencies
prepare temporary paths
invoke upstream build tooling
stream progress
capture logs
verify output
install output
manage updates
manage rollback
```

Aseprite-specific build logic should remain isolated and small.

Suggested package:

```text
internal/aseprite/
    release.go
    source.go
    requirements.go
    build.go
    verify.go
```

---

## 7. Licensing Constraint

This requirement is fundamental.

`aseprite-builder` distributes only its own executable.

It must not distribute compiled Aseprite binaries.

Required model:

```text
aseprite-builder binary
        ↓
official Aseprite source
        ↓
user's machine
        ↓
local compilation
        ↓
local Aseprite installation
```

Never:

```text
aseprite-builder release
        ↓
precompiled Aseprite binary
        ↓
user
```

Before the first installation, the application should display a concise notice explaining that:

- the builder is unofficial;
- Aseprite is a separate project;
- Aseprite will be compiled locally;
- the builder does not redistribute a compiled copy of Aseprite;
- the user remains responsible for complying with Aseprite's license.

Do not create unnecessary legal friction in the normal UI.

---

## 8. Goals

The project must:

1. Provide a simple interactive installation experience.
2. Support macOS, Windows, and Linux.
3. Build Aseprite locally from official source.
4. Default to stable Aseprite releases.
5. Detect the operating system and architecture automatically.
6. Detect required build dependencies.
7. Reuse compatible tools already present on the machine.
8. Download missing redistributable tools at runtime when appropriate.
9. Keep downloaded tools only in the user's local cache.
10. Resolve Aseprite-specific requirements dynamically whenever possible.
11. Compile Aseprite using upstream build tooling.
12. Install the compiled application using platform conventions.
13. Support future Aseprite updates without builder updates whenever the upstream contract remains compatible.
14. Preserve user preferences and data during updates.
15. Support repair and clean rebuild operations.
16. Support uninstalling installations created by this tool.
17. Provide useful diagnostics.
18. Keep the previously working installation intact if an update fails.
19. Require minimal technical knowledge from the user.
20. Expose the same capabilities through CLI and TUI.
21. Keep platform-specific behavior isolated behind clean interfaces.
22. Fail safely when an unknown upstream build contract is detected.

---

## 9. Non-Goals

The project must not:

- redistribute compiled Aseprite binaries;
- bundle Aseprite inside release artifacts;
- bundle CMake, Ninja, Skia, SDKs, installers, or toolchains;
- maintain a hard-coded dependency-version matrix when runtime discovery is possible;
- bypass or modify Aseprite licensing;
- behave as an alternative commercial Aseprite distribution;
- require Git for normal stable installation;
- require a third-party package manager simply to run;
- globally install downloaded runtime tools unless explicitly required;
- silently use elevated privileges;
- promise support for platforms that upstream Aseprite does not reasonably support;
- reimplement the upstream Aseprite build system without a strong technical reason.

---

## 10. Technology

### Language

Use Go.

Target a self-contained executable per supported platform.

Avoid requiring Node.js, Python, Java, .NET, or another runtime.

### TUI

Preferred library:

```text
Bubble Tea
```

Supporting Charm ecosystem libraries may be used where useful:

```text
Lip Gloss
Bubbles
```

The UI layer must not contain platform-specific or build-system business logic.

---

## 11. Supported Platforms

Initial architectural target:

```text
macOS
  - arm64 / Apple Silicon
  - amd64 / Intel

Windows
  - amd64

Linux
  - amd64 initially
  - arm64 where upstream requirements permit it
```

Initial Linux distribution targets:

```text
Debian
Ubuntu
Fedora
Arch Linux
openSUSE
Void Linux
```

Do not market the tool as supporting literally every operating system.

Preferred wording:

> Supports Aseprite's major desktop build platforms.

---

## 12. Normal User Experience

Running:

```bash
aseprite-builder
```

opens the TUI.

Example:

```text
┌──────────────────────────────────────────────┐
│ Aseprite Source Builder                      │
│ Unofficial community tool                    │
├──────────────────────────────────────────────┤
│                                              │
│ Aseprite is not installed.                   │
│                                              │
│ Latest stable: 1.x.x                         │
│                                              │
│ > Install Aseprite                           │
│   Advanced                                   │
│   Settings                                   │
│   Exit                                       │
│                                              │
└──────────────────────────────────────────────┘
```

During installation:

```text
Checking your system...

✓ Operating system
✓ Architecture
✓ Disk space
✓ Compiler/toolchain
✓ CMake            existing compatible version
✓ Ninja            downloading compatible version
✓ Aseprite source  resolved
✓ Skia             requirement discovered

Continue installation?
```

The user should not need to manually type build commands.

---

## 13. Installation Flow

Default installation flow:

```text
1. Detect system
2. Detect platform toolchain
3. Resolve latest stable Aseprite release
4. Download official source archive
5. Verify and extract source
6. Inspect upstream build contract
7. Discover required build dependencies
8. Reuse compatible system tools
9. Resolve/download missing runtime tools
10. Resolve/download required Skia artifact
11. Configure build environment
12. Invoke upstream build flow
13. Verify compiled executable
14. Create native application installation
15. Register relevant OS integration
16. Persist installation metadata
17. Clean temporary files
18. Optionally launch Aseprite
```

Each stage must emit structured progress events.

The TUI must not rely on parsing arbitrary human-readable compiler output to infer internal state.

---

## 14. Source Acquisition

### Stable Channel

Normal stable installation must not require Git.

Use the official Aseprite stable source distribution.

Conceptually:

```text
official release metadata
        ↓
official source archive
        ↓
local extraction
```

Persist useful metadata:

```text
version
source identifier / URL
source revision if available
download timestamp
checksum if authoritative checksum exists
build timestamp
```

### Development Channel

Development builds are an advanced feature.

They may require Git and track an explicit commit.

Example:

```text
Release channel

(*) Stable
( ) Development / main
```

Stable must always be the default.

If Git is absent, stable installation must still work.

---

## 15. Runtime Tool Cache

Downloaded runtime tools must live only in user-local cache/data directories.

Examples:

### macOS

```text
~/Library/Caches/aseprite-builder/
```

### Linux

Use XDG locations, e.g.:

```text
$XDG_CACHE_HOME/aseprite-builder/
```

or a standards-compliant fallback.

### Windows

Use appropriate `LOCALAPPDATA` locations.

Possible internal structure:

```text
cache/
    downloads/
    toolchains/
        cmake/
        ninja/
    skia/
    sources/
```

The cache is not part of the application distribution.

Downloaded tools should be reusable across future builds when still compatible.

---

## 16. System-Managed Toolchains

Some requirements must remain system-managed.

### macOS

Examples:

```text
Xcode
Apple Clang
macOS SDK
```

The builder detects them.

It must not ship Xcode or Apple SDK installers.

If missing, provide a clear native installation path or instructions.

### Windows

Examples:

```text
Visual Studio Build Tools
Desktop Development with C++
Windows SDK
```

The builder detects them.

It must not ship Microsoft installers inside its own release artifacts.

If automation is implemented, installers must be resolved at runtime from authoritative Microsoft sources.

### Linux

Compiler and system development libraries remain distribution-managed.

Use native package managers:

```text
apt
dnf
pacman
zypper
xbps
```

Never bundle `.deb`, `.rpm`, or distribution packages.

Never silently invoke `sudo`.

---

## 17. Compatibility Contract Detection

Before building, the application should verify that the Aseprite source tree exposes a build contract it understands.

Example:

```text
Checking Aseprite build interface...

✓ supported upstream build entry point detected
✓ required build metadata detected
✓ Skia requirement discoverable
✓ expected output contract recognized
```

If the contract is unknown:

```text
This Aseprite release uses a build layout that this
version of aseprite-builder does not recognize.

Your existing installation was not modified.

[ View details ]
[ Use installed version ]
[ Exit ]
```

The builder must never guess through an unknown build layout when doing so could corrupt or replace a working installation.

---

## 18. Platform Abstraction

Define OS-specific behavior behind stable interfaces.

Example:

```go
type Platform interface {
    Detect(context.Context) (SystemInfo, error)
    Requirements(context.Context) ([]Requirement, error)
    Prepare(context.Context) error

    Install(context.Context, BuildArtifact) error
    Launch(context.Context) error
    Uninstall(context.Context) error

    InstalledVersion(context.Context) (VersionInfo, error)
}
```

Suggested packages:

```text
internal/platform/darwin
internal/platform/windows
internal/platform/linux
```

Do not spread `runtime.GOOS` checks throughout the codebase.

---

## 19. Build Abstraction

Example:

```go
type Builder interface {
    Inspect(context.Context, SourceTree) (BuildContract, error)
    Prepare(context.Context, BuildRequest) error
    Build(context.Context, BuildRequest) (BuildResult, error)
    Verify(context.Context, BuildResult) error
}
```

The `BuildContract` should represent discovered upstream requirements rather than duplicated hard-coded release rules.

---

## 20. macOS

Support:

```text
Apple Silicon
Intel
```

Detect:

```text
macOS version
architecture
Xcode
selected developer directory
available SDK
disk space
```

Preferred user-local installation:

```text
~/Applications/Aseprite.app
```

The produced application bundle should behave like a normal macOS application.

Handle:

```text
Info.plist
executable location
resources
icon
document associations where appropriate
local/ad-hoc signing where appropriate
```

Do not present the locally built application as officially signed or notarized by Igara Studio.

---

## 21. Windows

Initial target:

```text
Windows 11 x64
```

Detect:

```text
Windows version
architecture
Visual Studio / Build Tools
C++ workload
required Windows SDK
disk space
```

Do not substitute MinGW for the upstream-supported Microsoft toolchain unless upstream officially supports such a path.

Preferred installation should be user-local where practical.

Example:

```text
%LOCALAPPDATA%\Programs\Aseprite\
```

Optional integration:

```text
Start Menu shortcut
desktop shortcut
file associations
```

Avoid administrator privileges unless genuinely required.

---

## 22. Linux

Detect distribution using standard metadata such as:

```text
/etc/os-release
```

Use a package-manager abstraction.

Example:

```go
type PackageManager interface {
    Detect(context.Context) bool
    MissingPackages(context.Context, []Package) ([]Package, error)
    Install(context.Context, []Package) error
}
```

Initial implementations:

```text
apt
dnf
pacman
zypper
xbps
```

Suggested user-local installation:

```text
~/.local/opt/aseprite/
```

CLI entry point:

```text
~/.local/bin/aseprite
```

Desktop integration:

```text
~/.local/share/applications/aseprite.desktop
```

---

## 23. Updates

Updates must be transactional.

Never destroy or overwrite the current working installation before the replacement has successfully built and passed verification.

Preferred flow:

```text
current installation
        ↓
resolve new stable source
        ↓
inspect build contract
        ↓
resolve dependencies dynamically
        ↓
build into separate directory
        ↓
verify
        ↓
prepare replacement
        ↓
safe swap
        ↓
retain previous version temporarily
```

Failure:

```text
Build failed.

Your existing Aseprite installation was not modified.

[ View log ]
[ Retry ]
[ Diagnostics ]
[ Back ]
```

---

## 24. Rollback

Where practical, retain:

```text
current
previous
```

Do not retain unlimited old builds by default.

Rollback should be possible when the new installation fails post-build verification or installation activation.

---

## 25. Repair

Repair should detect and fix common issues such as:

```text
missing executable
missing resources
broken application bundle
metadata mismatch
corrupted source cache
corrupted Skia cache
broken symlink
missing desktop entry
failed previous update
```

Repair actions must be explicit and logged.

---

## 26. Rebuild

Rebuild means:

```text
preserve user configuration/data
discard current build output
re-resolve current requirements
rebuild current selected Aseprite version
verify
replace installed application only after success
```

Support:

```text
Rebuild current version
Clean rebuild
```

---

## 27. Uninstall

Uninstall only files provably owned by `aseprite-builder`.

Never recursively delete unknown directories.

Possible user choices:

```text
Remove Aseprite application
Remove build cache
Remove downloaded source
Remove runtime tool cache
Remove builder configuration
```

Do not remove unrelated artwork or user-created project files.

---

## 28. Diagnostics / Doctor

Provide:

```bash
aseprite-builder doctor
```

and an equivalent TUI screen.

Example:

```text
Diagnostics

✓ OS                 macOS
✓ Architecture       arm64
✓ Compiler           Apple Clang
✓ Xcode              detected
✓ SDK                detected
✓ CMake              compatible system version
✓ Ninja              builder cache
✓ Skia               requirement resolved
✓ Source             valid
✓ Build contract     recognized
✓ Installation       valid
✓ CLI integration    valid

Everything looks good.
```

Also support:

```bash
aseprite-builder doctor --json
```

Do not include sensitive data unnecessarily.

---

## 29. Logging

Every install/update/rebuild should create a log.

Include:

```text
builder version
OS
architecture
selected Aseprite version
discovered build contract
resolved dependency versions
dependency source locations
commands executed
exit codes
build output
error context
```

Redact:

```text
passwords
tokens
credentials
unrelated environment variables
```

---

## 30. CLI

Interactive:

```bash
aseprite-builder
```

Commands:

```bash
aseprite-builder install
aseprite-builder update
aseprite-builder launch
aseprite-builder repair
aseprite-builder rebuild
aseprite-builder uninstall
aseprite-builder doctor
aseprite-builder version
```

Advanced examples:

```bash
aseprite-builder install --channel stable
aseprite-builder install --version <version>
aseprite-builder update --yes
aseprite-builder rebuild --clean
aseprite-builder doctor --json
```

CLI and TUI must use the same application services.

Do not duplicate business logic.

---

## 31. Process Execution

Create a centralized runner.

Example:

```go
type Runner interface {
    Run(context.Context, Command) (Result, error)
}
```

Requirements:

```text
stream stdout/stderr
capture output for logs
support cancellation
record exit codes
avoid shell interpolation where unnecessary
handle spaces safely
```

Prefer direct process execution over shell command strings.

---

## 32. Networking

Downloads must be handled internally.

Requirements:

```text
HTTPS
redirect handling
timeouts
progress reporting
partial temporary files
atomic completion
retry support
verification where authoritative checksums/signatures exist
```

Never trust partially downloaded archives.

Remote metadata must never be allowed to inject arbitrary commands.

---

## 33. Security

Treat the following as untrusted input:

```text
downloaded archives
archive paths
release metadata
filesystem paths
environment variables
remote filenames
```

Prevent:

```text
archive path traversal
shell injection
unexpected overwrite outside managed directories
unsafe privilege escalation
```

Commands must come from trusted application logic.

---

## 34. TUI Architecture

Possible UI states:

```text
Welcome
Home
SystemCheck
InstallOptions
Installing
Installed
UpdateAvailable
Updating
Repair
Diagnostics
Settings
Advanced
Error
Confirmation
```

Prefer a state-machine architecture.

The TUI consumes structured application events.

Example:

```go
type ProgressEvent struct {
    Stage   Stage
    Current int64
    Total   int64
    Message string
}
```

Application services must not import Bubble Tea.

Dependency direction:

```text
TUI / CLI
    ↓
application services
    ↓
platform / source / dependency / build infrastructure
```

---

## 35. Suggested Repository Layout

```text
.
├── cmd/
│   └── aseprite-builder/
│       └── main.go
│
├── internal/
│   ├── app/
│   │   ├── install.go
│   │   ├── update.go
│   │   ├── repair.go
│   │   ├── rebuild.go
│   │   ├── uninstall.go
│   │   └── doctor.go
│   │
│   ├── aseprite/
│   │   ├── release.go
│   │   ├── source.go
│   │   ├── requirements.go
│   │   ├── build.go
│   │   └── verify.go
│   │
│   ├── platform/
│   │   ├── platform.go
│   │   ├── darwin/
│   │   ├── windows/
│   │   └── linux/
│   │
│   ├── dependencies/
│   │   ├── resolver.go
│   │   ├── cmake/
│   │   ├── ninja/
│   │   └── skia/
│   │
│   ├── packages/
│   │   ├── apt/
│   │   ├── dnf/
│   │   ├── pacman/
│   │   ├── zypper/
│   │   └── xbps/
│   │
│   ├── download/
│   ├── archive/
│   ├── runner/
│   ├── state/
│   ├── paths/
│   ├── logging/
│   └── tui/
│
├── docs/
│   ├── PROJECT_SPEC.md
│   ├── ARCHITECTURE.md
│   └── DEVELOPMENT.md
│
├── testdata/
│
├── go.mod
├── go.sum
├── README.md
├── LICENSE
└── Makefile
```

---

## 36. Repository Hygiene

The project must not commit:

```text
dist/
build/
.cache/
tmp/
downloads/
toolchains/
sources/
skia/
vendor-bin/
Aseprite.app
aseprite.exe
third-party archives
installer executables
SDK payloads
```

Use `.gitignore` accordingly.

Go library dependencies may be tracked normally through:

```text
go.mod
go.sum
```

This does not violate the zero-vendored-toolchain principle.

---

## 37. Testing Strategy

Unit-test behavior without compiling Aseprite in every test.

Test:

```text
version parsing
release selection
runtime dependency resolution
system-tool preference
cache fallback
build contract detection
state transitions
path resolution
package-manager selection
installation metadata
archive validation
command generation
rollback decisions
```

Use a fake process runner.

Example:

```go
type FakeRunner struct {
    Calls   []Command
    Results map[string]Result
}
```

Integration tests may cover:

```text
archive extraction
downloads
filesystem installation
CLI behavior
cancellation
runtime cache behavior
```

CI may run platform-specific tests on macOS, Windows, and Linux.

Do not publish compiled Aseprite test artifacts.

---

## 38. Release Safety

Builder release artifacts must contain only project-owned output.

Add automated release checks that fail if artifacts contain names or paths associated with:

```text
Aseprite binaries
Skia packages
CMake distributions
Ninja distributions
Visual Studio installers
SDK installers
source archives
runtime caches
```

---

## 39. Failure UX

Do not use raw build output as the primary error message.

Bad:

```text
exit status 1
```

Better:

```text
Aseprite could not be compiled.

The upstream build process failed.

Your existing installation was not modified.

> Run diagnostics
  View build log
  Retry
  Back
```

If the failure is caused by an unrecognized upstream contract, say so explicitly.

---

## 40. Standard User Principle

Every feature should be evaluated against:

> Could someone who has never used a compiler understand what to do next?

Avoid exposing:

```text
CMAKE_OSX_SYSROOT
LAF_BACKEND
SKIA_LIBRARY_DIR
vcvars64.bat
compiler flags
```

unless the user intentionally opens an advanced/debug screen.

---

## 41. Advanced Mode

Advanced users may optionally configure:

```text
release channel
specific Aseprite version
source retention
build retention
custom install path
system-vs-cached tool preference
verbose logs
clean rebuild
development source checkout
```

Advanced options must not clutter the default workflow.

---

## 42. Development Milestones

### Milestone 1 — Platform-Independent Core

Implement:

```text
Go module
CLI entry point
configuration
paths
logging
process runner
release/version models
download infrastructure
archive extraction
runtime dependency resolver
cache model
build-contract model
tests
```

### Milestone 2 — macOS arm64 Vertical Slice

Implement:

```text
system detection
Xcode detection
stable Aseprite source resolution
runtime CMake/Ninja reuse-or-download
Skia requirement discovery
build-contract inspection
Aseprite build
Aseprite.app creation
installation
launch
update
rebuild
repair
uninstall
doctor
```

This is the first end-to-end platform.

### Milestone 3 — TUI

Build the interactive UI on top of already-working application services.

Do not put installer logic inside TUI models.

### Milestone 4 — Windows x64

Implement:

```text
Visual Studio detection
Windows SDK detection
runtime tool resolution
build environment
native installation
shortcuts/integration
```

### Milestone 5 — Debian/Ubuntu

Implement:

```text
Linux platform
apt dependency handling
desktop integration
CLI integration
```

### Milestone 6 — Additional Linux Distributions

Add:

```text
dnf
pacman
zypper
xbps
```

### Milestone 7 — Hardening

Implement:

```text
rollback
failure recovery
download verification
compatibility contract validation
better diagnostics
cancellation
security review
CI matrix
release safety checks
```

---

## 43. MVP Acceptance Criteria

The first macOS arm64 MVP is complete when a user can:

1. Download one `aseprite-builder` executable.
2. Run it.
3. Select Install.
4. Receive a clear message if system-managed prerequisites are missing.
5. Have the tool resolve official stable Aseprite source.
6. Reuse compatible CMake/Ninja if already installed.
7. Download compatible CMake/Ninja at runtime if needed.
8. Discover the required Skia configuration from upstream data.
9. Download required Skia at runtime.
10. Compile Aseprite locally.
11. Receive a normal `Aseprite.app`.
12. Launch it without manually entering build commands.
13. Run the builder again and see the installed version.
14. Update to future compatible stable Aseprite releases without changing builder code.
15. Rebuild the current version.
16. Run diagnostics.
17. Uninstall the builder-managed Aseprite application.

No step should require the user to manually execute:

```text
git
cmake
ninja
codesign
```

commands.

---

## 44. Cross-Platform Definition of Done

The cross-platform release is complete when the same conceptual workflow works on:

```text
macOS
Windows
Linux
```

and users can perform:

```text
install
launch
update
repair
rebuild
doctor
uninstall
```

through both:

```text
TUI
CLI
```

while guaranteeing:

```text
no precompiled Aseprite binaries are distributed
no third-party installers/toolchains are bundled
dependencies are resolved at runtime
compatible local tools are reused
failed updates do not destroy working installations
unknown upstream build contracts fail safely
platform-specific logic is isolated
normal users do not need build-system knowledge
ordinary upstream version bumps do not require builder releases
```

---

## 45. Development Rules for Coding Agents

When working on this project:

1. Read this specification before making architectural changes.
2. Preserve the zero-vendored-toolchain principle.
3. Do not commit or ship third-party executables, installers, SDKs, source archives, Skia packages, or compiled Aseprite binaries.
4. Prefer runtime discovery over hard-coded version tables.
5. Prefer compatible existing system tools before downloading cached alternatives.
6. Resolve missing downloadable tools only from authoritative upstream sources.
7. Keep downloaded dependencies in user-local cache only.
8. Treat platform toolchains such as Xcode, Visual Studio, SDKs, and Linux system packages as system-managed.
9. Keep OS-specific behavior behind platform interfaces.
10. Keep UI logic separate from application services.
11. Do not add a dependency without a concrete need.
12. Prefer Go standard library functionality when reasonable.
13. Never silently invoke privileged commands.
14. Never delete directories that are not provably owned by this project.
15. Never replace a working installation before a new build succeeds and verifies.
16. Add tests for non-trivial behavior.
17. Do not create abstractions only for hypothetical future requirements.
18. Treat upstream Aseprite build metadata/tooling as the primary source of truth.
19. Do not duplicate Aseprite's dependency matrix if it can be discovered dynamically.
20. If the upstream build contract is unknown, fail safely.
21. Do not claim success until the installed executable has been verified.
22. Keep normal-user UX substantially simpler than the underlying build process.
23. Design so ordinary upstream version bumps do not require builder changes.
24. A builder update is justified by a contract break, bug fix, security issue, or builder feature—not merely by a new dependency version.

---

## 46. First Agent Task

Start with the platform-independent foundation.

Do **not** implement all operating systems at once.

Implement:

```text
1. Go module and repository skeleton
2. CLI entry point
3. platform interface
4. process runner
5. filesystem/path management
6. config/state persistence
7. logging
8. release/version domain models
9. source download abstraction
10. archive extraction
11. runtime dependency resolver
12. system-tool detection
13. user-local dependency cache
14. build-contract inspection model
15. install/update service interfaces
16. unit tests
```

Then implement macOS arm64 as the first vertical slice.

The vertical slice must prove:

```text
official stable source
        ↓
inspect upstream build contract
        ↓
detect system tools
        ↓
reuse or runtime-download CMake/Ninja
        ↓
discover Skia requirement
        ↓
resolve Skia
        ↓
build
        ↓
native app installation
        ↓
launch
        ↓
update
        ↓
uninstall
```

Do not start Windows/Linux implementation or spend significant effort polishing the TUI until the CLI installation path works end-to-end.

The first important command is:

```bash
aseprite-builder install
```

Once that is reliable, expose the exact same service through the TUI.

---

## 47. Product Principle

The complexity belongs inside the application, not in the user's instructions.

The ideal end-user documentation should eventually be almost this short:

```text
1. Download Aseprite Source Builder.
2. Run it.
3. Choose Install Aseprite.
```

Everything else is an implementation detail.
