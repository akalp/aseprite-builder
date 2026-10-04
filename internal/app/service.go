// Package app contains the application service layer: every operation
// (install, update, rebuild, repair, uninstall, doctor, launch) is
// realized here and shared by the CLI and the future TUI.
//
// Services never touch runtime.GOOS — all platform specifics arrive
// through the platform.Platform interface — and never contain UI code.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/dependencies"
	"github.com/akalp/aseprite-builder/internal/download"
	"github.com/akalp/aseprite-builder/internal/logging"
	"github.com/akalp/aseprite-builder/internal/paths"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
	"github.com/akalp/aseprite-builder/internal/state"
	"github.com/akalp/aseprite-builder/internal/upstream"
	"github.com/akalp/aseprite-builder/internal/version"
)

// Stage enumerates install pipeline stages; the TUI consumes these
// in the same order the CLI prints them.
type Stage int

// Pipeline stages in execution order.
const (
	StageDetect Stage = iota + 1
	StageSystemRequirements
	StageResolveRelease
	StageFetchSource
	StageInspectContract
	StageResolveTools
	StageResolveSkia
	StageBuild
	StageVerify
	StageInstall
	StageCleanup
)

// stageNames are human-readable labels for logs and progress.
var stageNames = map[Stage]string{
	StageDetect:             "Detect system",
	StageSystemRequirements: "Check system requirements",
	StageResolveRelease:     "Resolve Aseprite release",
	StageFetchSource:        "Fetch official source",
	StageInspectContract:    "Inspect upstream build contract",
	StageResolveTools:       "Resolve build tools",
	StageResolveSkia:        "Resolve Skia",
	StageBuild:              "Compile Aseprite",
	StageVerify:             "Verify build result",
	StageInstall:            "Install application",
	StageCleanup:            "Clean up",
}

// String renders the stage name.
func (s Stage) String() string { return stageNames[s] }

// ProgressEvent is one structured progress notification. The TUI shows
// these; the CLI prints them one per line.
type ProgressEvent struct {
	Stage   Stage
	Message string
	Current int64
	Total   int64
}

// Build sub-phase identifiers used in StageBuild progress events
// (mirroring the phases the upstream build reports).
const (
	BuildPhaseConfigure = "configure"
	BuildPhaseCompile   = "compile"
)

// ProgressSink receives progress events. It must be cheap: services
// emit synchronously.
type ProgressSink func(ProgressEvent)

// emit is a nil-safe helper.
func (s ProgressSink) emit(e ProgressEvent) {
	if s != nil {
		s(e)
	}
}

// Options wires every dependency of the service layer.
type Options struct {
	// BuilderVersion is this executable's own version string.
	BuilderVersion string
	// Layout are the resolved user-local directories.
	Layout paths.Layout
	// Platform is the concrete OS implementation.
	Platform platform.Platform
	// Releases resolves Aseprite releases from upstream metadata.
	Releases aseprite.ReleaseProvider
	// Upstream is the release metadata client (Skia digests and tool
	// resolution use it). Nil falls back to upstream.New().
	Upstream *upstream.Client
	// Runner is the default process runner for probes.
	Runner runner.Runner
	// Store persists configuration and install state.
	Store state.Store
	// FetchHook is a test-only override of source download.
	fetchHook func(ctx context.Context, src, dst, sha string, progress func(int64, int64)) error
	// BuildRunnerOverride is a test-only runner used for the upstream
	// build invocation, ensuring tests never execute build scripts.
	BuildRunnerOverride runner.Runner
	// LogDirOverride is used by tests; empty uses Layout.Logs.
	logDir string
}

// Service implements the application operations.
type Service struct {
	opts Options
}

// New validates options and returns a Service.
func New(opts Options) (*Service, error) {
	if opts.Layout.Root == "" {
		return nil, errors.New("app: layout is not set")
	}
	if opts.Platform == nil {
		return nil, errors.New("app: platform is not set")
	}
	if opts.Releases == nil {
		return nil, errors.New("app: release provider is not set")
	}
	if opts.Runner == nil {
		opts.Runner = runner.NewConcreteRunner()
	}
	return &Service{opts: opts}, nil
}

// InstallOptions selects what install performs.
type InstallOptions struct {
	// Version pins an exact Aseprite version ("" → latest stable).
	Version string
	// Channel is stable today.
	Channel aseprite.Channel
	// ForceOverwriteUnowned allows replacing an Aseprite.app this
	// builder cannot prove it owns (explicit user consent).
	ForceOverwriteUnowned bool
	// LaunchAfterInstall opens Aseprite when the install finishes.
	LaunchAfterInstall bool
	// KeepWorkspace retains the build workspace (for rebuild/repair).
	KeepWorkspace bool
}

// InstallResult summarizes a completed install/update.
type InstallResult struct {
	Version      string
	AppPath      string
	PreviousPath string // retained previous installation path, if any
	Workspace    string
	Contract     string // discovered build contract fingerprint
	SkiaTag      string
	LogPath      string
}

// Typed errors the CLI maps to friendly user-facing messages.
var (
	// ErrAlreadyInstalled: requested version already installed.
	ErrAlreadyInstalled = errors.New("this Aseprite version is already installed")
	// ErrUpToDate: no newer stable release than the installed one.
	ErrUpToDate = errors.New("aseprite is up to date")
	// ErrNotInstalled: operation needs an existing installation.
	ErrNotInstalled = errors.New("no aseprite-builder installation found")
	// ErrSystemRequirements: system-managed prerequisites missing.
	ErrSystemRequirements = errors.New("system requirements are not met")
	// ErrUnknownContract: the upstream build contract is unrecognized.
	ErrUnknownContract = errors.New("unrecognized upstream build contract")
)

// SystemRequirementsError carries the failing checks to the CLI.
type SystemRequirementsError struct {
	Checks []platform.Check
}

func (e SystemRequirementsError) Error() string {
	var failing []string
	for _, c := range e.Checks {
		if !c.OK {
			failing = append(failing, c.Name)
		}
	}
	return fmt.Sprintf("system requirements are not met: %s", strings.Join(failing, ", "))
}

// Unwrap exposes ErrSystemRequirements so callers can errors.Is.
func (e SystemRequirementsError) Unwrap() error { return ErrSystemRequirements }

// NewSystemRequirementsErrorForTest constructs a sample requirements
// failure for CLI message-mapping tests.
func NewSystemRequirementsErrorForTest() error {
	return SystemRequirementsError{Checks: []platform.Check{
		{Name: "Xcode", OK: false, Fix: "xcode-select --install"},
		{Name: "Disk", OK: true},
	}}
}

// UnknownContractError wraps inspection failures for CLI mapping.
type UnknownContractError struct {
	Reason error
}

func (e UnknownContractError) Error() string {
	return fmt.Sprintf("%s: %v", ErrUnknownContract, e.Reason)
}

func (e UnknownContractError) Unwrap() error { return ErrUnknownContract }

// Install runs the full install pipeline (used by install, update and
// rebuild with different inputs).
func (s *Service) Install(ctx context.Context, opts InstallOptions, sink ProgressSink) (InstallResult, error) {
	if err := s.opts.Layout.EnsureDirs(); err != nil {
		return InstallResult{}, err
	}
	log, logPath, err := logging.NewFile(s.logDir(), "install")
	if err != nil {
		return InstallResult{}, err
	}
	defer func() { _ = log.Close() }()
	log.Line("aseprite-builder %s: install started", s.opts.BuilderVersion)

	cfg := s.runConfig()
	applyRunConfig(&opts, cfg)
	result, err := s.runPipeline(ctx, opts, verboseSink(sink, cfg, log), log)
	if err != nil {
		result.LogPath = logPath
		log.Line("install failed: %v", err)
		return result, err
	}
	result.LogPath = logPath
	log.Line("install completed: Aseprite %s installed at %s", result.Version, result.AppPath)
	return result, nil
}

func (s *Service) logDir() string {
	if s.opts.logDir != "" {
		return s.opts.logDir
	}
	return s.opts.Layout.Logs
}

// runPipeline executes stages 1..N shared by install/update/rebuild.
func (s *Service) runPipeline(ctx context.Context, opts InstallOptions, sink ProgressSink, log *logging.Logger) (InstallResult, error) {
	var result InstallResult

	// 1. Detect system ------------------------------------------------
	sink.emit(ProgressEvent{Stage: StageDetect, Message: "Checking operating system and architecture"})
	info, err := s.opts.Platform.Detect(ctx)
	if err != nil {
		return result, fmt.Errorf("detect system: %w", err)
	}
	log.Line("system: %s %s (%s)", info.OS, info.OSVersion, info.Arch)
	if !info.Supported {
		return result, fmt.Errorf("platform %s/%s is not supported by this release of aseprite-builder", info.OS, info.Arch)
	}

	// 2. System-managed requirements ----------------------------------
	sink.emit(ProgressEvent{Stage: StageSystemRequirements, Message: "Checking system-managed build requirements"})
	checks, err := s.opts.Platform.Requirements(ctx)
	if err != nil {
		return result, fmt.Errorf("check requirements: %w", err)
	}
	for _, c := range checks {
		log.Line("requirement: %s", c.String())
		if !c.OK {
			return result, SystemRequirementsError{Checks: checks}
		}
	}

	// 3. Resolve release ------------------------------------------------
	sink.emit(ProgressEvent{Stage: StageResolveRelease, Message: "Resolving latest stable Aseprite release"})
	rel, err := s.resolveRelease(ctx, opts)
	if err != nil {
		return result, err
	}
	log.Line("resolved release: %s", rel.String())
	result.Version = rel.Version.String()
	result.Workspace = s.opts.Layout.SourceTreeDir(result.Version)

	// 4/5. Source acquisition + contract inspection ---------------------
	provisioned, err := s.provisionSource(ctx, rel, sink, log)
	if err != nil {
		return result, err
	}
	result.Contract = provisioned.Contract.ContractFingerprint()
	result.SkiaTag = provisioned.Contract.SkiaTag

	// 6/7/8. Tools + Skia -------------------------------------------------
	buildPlan, err := s.resolveBuildInputs(ctx, provisioned, info, sink, log)
	if err != nil {
		return result, err
	}

	// 9. Build ------------------------------------------------------------
	// The Builder's first progress tick announces the configure phase;
	// no separate announcement is needed here (the HUD shows
	// "Configuring build" until ninja begins reporting counters).
	log.Line("build: invoking upstream build flow in %s", provisioned.SourceDir)
	builder := aseprite.Builder{
		Runner: s.opts.BuildRunnerOverride, // non-nil in tests (fakes)
		Stream: log.Writer(),
		Log:    log,
		Progress: func(p aseprite.BuildProgress) {
			phase := p.Phase
			switch phase {
			case aseprite.BuildPhaseConfigure:
				phase = BuildPhaseConfigure
			case aseprite.BuildPhaseCompile:
				phase = BuildPhaseCompile
			}
			sink.emit(ProgressEvent{
				Stage:   StageBuild,
				Message: phase,
				Current: p.Done,
				Total:   p.Total,
			})
		},
	}
	if err := s.opts.Platform.Prepare(ctx); err != nil {
		return result, fmt.Errorf("platform prepare: %w", err)
	}
	// Platforms that manage their own toolchain environment (Windows:
	// the VS environment captured from vcvars64.bat) are consulted via
	// the optional interface; detection failures abort before build.
	buildEnv, err := s.captureBuildEnvironment(ctx, log)
	if err != nil {
		return result, err
	}
	cpu := skiaCPUFor(info.Arch)
	buildOut, err := builder.Build(ctx, aseprite.BuildRequest{
		SourceDir:    provisioned.SourceDir,
		Contract:     provisioned.Contract,
		SkiaDir:      buildPlan.skiaDir,
		CPU:          cpu,
		ExtraBinDirs: buildPlan.extraBinDirs(),
		Env:          buildEnv,
	})
	if err != nil {
		return result, err
	}
	log.Line("build produced %s", buildOut.AppPath)
	if buildOut.AppPath == "" {
		log.Line("build produced executable %s (data at %s)", buildOut.Executable, buildOut.DataDir)
	}

	// 10. Verify ------------------------------------------------------------
	sink.emit(ProgressEvent{Stage: StageVerify, Message: "Verifying the compiled executable"})
	if err := aseprite.VerifyBuild(ctx, s.opts.Runner, buildOut, result.Version); err != nil {
		return result, fmt.Errorf("verification failed: %w", err)
	}
	log.Line("verification succeeded")

	// 11. Install (transactional) -------------------------------------------
	sink.emit(ProgressEvent{Stage: StageInstall, Message: "Installing Aseprite"})
	if installer, ok := s.opts.Platform.(interface {
		SetAllowReplaceUnowned(bool)
	}); ok {
		installer.SetAllowReplaceUnowned(opts.ForceOverwriteUnowned)
	}
	outcome, err := s.opts.Platform.Install(ctx, platform.BuildOutput{
		Version:    result.Version,
		AppPath:    buildOut.AppPath,
		Executable: buildOut.Executable,
		BinDir:     buildOut.BinDir,
		DataDir:    buildOut.DataDir,
		IconsDir:   buildOut.IconsDir,
	})
	if err != nil {
		return result, fmt.Errorf("install failed: %w", err)
	}
	log.Line("installed %s (previous retained: %q)", outcome.AppPath, outcome.PreviousPath)
	result.AppPath = outcome.AppPath
	result.PreviousPath = outcome.PreviousPath

	// 12. Persist state -------------------------------------------------------
	st := state.InstallState{
		AsepriteVersion: result.Version,
		TagName:         rel.TagName,
		Channel:         string(opts.Channel),
		SourceURL:       rel.SourceURL,
		SourceSHA256:    rel.SHA256,
		SkiaTag:         provisioned.Contract.SkiaTag,
		BuildContract:   provisioned.Contract.ContractFingerprint(),
		AppPath:         outcome.AppPath,
		InstalledAt:     nowUTC(),
		PreviousAppPath: outcome.PreviousPath,
		WorkspacePath:   result.Workspace,
	}
	if err := s.opts.Store.SaveInstall(st); err != nil {
		return result, fmt.Errorf("persist installation state: %w", err)
	}

	// 13. Cleanup -----------------------------------------------------------
	sink.emit(ProgressEvent{Stage: StageCleanup, Message: "Cleaning temporary files"})
	if !opts.KeepWorkspace {
		if err := os.RemoveAll(filepath.Join(result.Workspace, "build")); err != nil {
			log.Line("note: could not remove build dir: %v", err)
		}
	}
	if err := s.pruneOldSources(result.Workspace, log); err != nil {
		log.Line("note: source tree pruning issue: %v", err)
	}

	// 14. Optional launch
	if opts.LaunchAfterInstall {
		if err := s.opts.Platform.Launch(ctx); err != nil {
			log.Line("note: launch after install failed: %v", err)
		}
	}
	return result, nil
}

// resolveRelease picks the release to build: the pinned version when
// requested, otherwise the latest stable release upstream.
func (s *Service) resolveRelease(ctx context.Context, opts InstallOptions) (aseprite.Release, error) {
	if opts.Version == "" {
		rel, err := s.opts.Releases.Resolve(ctx, opts.Channel)
		if err != nil {
			return aseprite.Release{}, fmt.Errorf("resolve latest Aseprite release: %w", err)
		}
		return rel, nil
	}
	v, err := aseprite.ParseVersion(opts.Version)
	if err != nil {
		return aseprite.Release{}, fmt.Errorf("invalid Aseprite version %q: %w", opts.Version, err)
	}
	releases, err := s.opts.Releases.List(ctx, opts.Channel)
	if err != nil {
		return aseprite.Release{}, err
	}
	for _, r := range releases {
		if r.Version.Compare(v) == 0 {
			return r, nil
		}
	}
	return aseprite.Release{}, fmt.Errorf("Aseprite release %s not found in the official stable releases", opts.Version)
}

// provisionResult bundles what source provisioning produces.
type provisionResult struct {
	SourceDir string
	Contract  aseprite.Contract
	// Reused is true when the extracted source was already present.
	Reused bool
}

func (s *Service) defaultFetcher() fetcher {
	if s.opts.fetchHook != nil {
		return fetcherAdapter{s.opts.fetchHook}
	}
	return downloaderFetcher{s.newDownloader()}
}

// fetcher abstracts download.Downloader for tests.
type fetcher interface {
	Fetch(ctx context.Context, src, dst, expectedSHA256 string, progress func(done, total int64)) error
}

type fetcherAdapter struct {
	fn func(ctx context.Context, src, dst, sha string, progress func(int64, int64)) error
}

func (a fetcherAdapter) Fetch(ctx context.Context, src, dst, sha string, progress func(int64, int64)) error {
	return a.fn(ctx, src, dst, sha, progress)
}

// downloaderFetcher adapts download.Downloader's named Progress type to
// the local fetcher interface.
type downloaderFetcher struct{ dl *download.Downloader }

func (f downloaderFetcher) Fetch(ctx context.Context, src, dst, sha string, progress func(done, total int64)) error {
	return f.dl.Fetch(ctx, src, dst, sha, download.Progress(progress))
}

// provisionSource downloads (unless cached) and extracts the official
// source, then inspects the upstream build contract. Failure at this
// stage is always safe: nothing has been installed.
//
// Inspection runs on EVERY provision, including reused trees: the
// upstream contract may have changed since the last build, and the
// build orchestration needs contract facts (Skia naming, CMake
// minimum) even when the tree is cached.
func (s *Service) provisionSource(ctx context.Context, rel aseprite.Release, sink ProgressSink, log *logging.Logger) (provisionResult, error) {
	sink.emit(ProgressEvent{Stage: StageFetchSource, Message: "Downloading official source archive"})

	sourceDir := s.opts.Layout.SourceTreeDir(rel.Version.String())
	reused := true
	if _, statErr := os.Stat(filepath.Join(sourceDir, "CMakeLists.txt")); statErr != nil {
		reused = false
		if err := os.MkdirAll(s.opts.Layout.Downloads, 0o755); err != nil {
			return provisionResult{}, err
		}
		archivePath := s.opts.Layout.DownloadFile("aseprite", rel.SourceAssetName())
		fetch := s.defaultFetcher()
		if err := fetch.Fetch(ctx, rel.SourceURL, archivePath, rel.SHA256, nil); err != nil {
			return provisionResult{}, fmt.Errorf("download Aseprite source: %w", err)
		}
		if err := os.RemoveAll(sourceDir); err != nil {
			return provisionResult{}, fmt.Errorf("prepare source dir: %w", err)
		}
		if err := extractSourceArchive(archivePath, sourceDir); err != nil {
			return provisionResult{}, err
		}
		if err := os.Remove(archivePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Line("note: could not remove source archive: %v", err)
		}
	} else {
		log.Line("source for %s already extracted at %s (reused)", rel.Version, sourceDir)
	}

	sink.emit(ProgressEvent{Stage: StageInspectContract, Message: "Inspecting upstream build contract"})
	inspector := aseprite.NewInspector()
	contract, err := inspector.Inspect(sourceDir, s.wantsOutputConfig())
	if err != nil {
		return provisionResult{}, UnknownContractError{Reason: err}
	}
	log.Line("build contract: %s", contract.ContractFingerprint())
	s.noteVerification(rel, log)

	return provisionResult{
		SourceDir: sourceDir,
		Contract:  contract,
		Reused:    reused,
	}, nil
}

// noteVerification records when a source archive is downloaded without
// an authoritative digest (e.g. rate-limit fallback), so the install
// log always states the verification level honestly.
func (s *Service) noteVerification(rel aseprite.Release, log *logging.Logger) {
	if rel.SHA256 == "" {
		log.Line("note: source downloaded without an authoritative digest (upstream metadata did not provide one); " +
			"integrity is still enforced by contract inspection before building")
	} else {
		log.Line("source digest verified: sha256:%s", shortestDigest(rel.SHA256))
	}
}

// shortestDigest keeps logs readable.
func shortestDigest(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}

// skiaResolutionNeedsBashFree reports whether the platform cannot run
// the upstream bash resolver (Windows; stock Linux/macOS can).
func (s *Service) skiaResolutionNeedsBashFree(osName string) bool {
	return osName == "windows"
}

// BuildEnvironmentProvider is implemented by platforms that must
// assemble a toolchain environment before a build runs (Windows: the
// Visual Studio environment from vcvars64.bat).
type BuildEnvironmentProvider interface {
	BuildEnvironment(ctx context.Context) ([]string, error)
}

// captureBuildEnvironment returns the platform's build environment;
// plain environments yield nil without consulting the platform.
func (s *Service) captureBuildEnvironment(ctx context.Context, log *logging.Logger) ([]string, error) {
	provider, ok := s.opts.Platform.(BuildEnvironmentProvider)
	if !ok {
		return nil, nil
	}
	env, err := provider.BuildEnvironment(ctx)
	if err != nil {
		return nil, fmt.Errorf("platform build environment: %w", err)
	}
	log.Line("platform build environment captured (%d entries)", len(env))
	return env, nil
}

// wantsOutputConfig selects which upstream output contract the
// running platform consumes: the macOS .app bundle or the Windows
// exe + bin/data layout. Bundling is part of the discovered contract
// only on systems that consume it.
func (s *Service) wantsOutputConfig() aseprite.ExeOutputConfig {
	path := s.opts.Platform.DefaultInstallPath()
	if path != "" && strings.HasSuffix(path, ".app") {
		return aseprite.ExeOutputAppBundle
	}
	return aseprite.ExeOutputWindows
}

// extractSourceArchive extracts the source zip into sourceDir,
// handling both flat archives and archives with a single wrapper dir.
func extractSourceArchive(archivePath, sourceDir string) error {
	if err := extractArchive(archivePath, sourceDir); err != nil {
		return fmt.Errorf("extract source archive: %w", err)
	}
	// The official source zip is flat (verified contract); but handle a
	// single wrapper directory generically by hoisting its contents.
	return flattenSingleWrapper(sourceDir)
}

// buildPlan holds resolved inputs for the build stage.
type buildPlan struct {
	cmake    dependencies.Tool
	ninja    dependencies.Tool
	skiaDir  string
	binDirs  []string
	buildEnv []string
}

func (p buildPlan) extraBinDirs() []string {
	dirs := []string{}
	for _, t := range []dependencies.Tool{p.cmake, p.ninja} {
		if t.BinDir != "" && !containsDir(dirs, t.BinDir) {
			dirs = append(dirs, t.BinDir)
		}
	}
	return dirs
}

func containsDir(dirs []string, d string) bool {
	for _, x := range dirs {
		if strings.EqualFold(x, d) {
			return true
		}
	}
	return false
}

// resolveBuildInputs runs stages: tools (system→cache→download) and
// Skia (requirement discovered from the source tree).
func (s *Service) resolveBuildInputs(ctx context.Context, provisioned provisionResult, info platform.SystemInfo, sink ProgressSink, log *logging.Logger) (buildPlan, error) {
	var plan buildPlan

	minCMake := provisioned.Contract.CMakeMinVersion
	// Download flow selection: the Windows fetchers resolve the
	// Windows distributions (cmake-windows-x86_64.zip, ninja-win.zip).
	var cmakeFetcher dependencies.Fetcher = dependencies.CMakeFetcher{
		Layout: s.opts.Layout,
		Client: s.upstreamClient(),
		Log:    log.Line,
	}
	var ninjaFetcher dependencies.Fetcher = dependencies.NinjaFetcher{
		Layout: s.opts.Layout,
		Client: s.upstreamClient(),
		Log:    log.Line,
	}
	if info.OS == "windows" {
		cmakeFetcher = dependencies.NewWindowsCMakeFetcher(s.opts.Layout, s.upstreamClient())
		ninjaFetcher = dependencies.NewWindowsNinjaFetcher(s.opts.Layout, s.upstreamClient())
	}
	resolver := &dependencies.Resolver{
		Runner: s.opts.Runner,
		Layout: s.opts.Layout,
		Fetchers: map[string]dependencies.Fetcher{
			"cmake": cmakeFetcher,
			"ninja": ninjaFetcher,
		},
		MinVersions: map[string]version.Version{
			"cmake": minCMake,
			// Ninja has no minimum in the upstream contract; presence of
			// any working version qualifies.
			"ninja": version.Version{Parts: []int{1, 8}},
		},
	}

	sink.emit(ProgressEvent{Stage: StageResolveTools, Message: "Resolving CMake and Ninja"})
	cmake, err := resolver.Resolve(ctx, "cmake")
	if err != nil {
		return plan, fmt.Errorf("resolve CMake: %w", err)
	}
	plan.cmake = cmake
	log.Line("tool resolved: %s", cmake)
	ninja, err := resolver.Resolve(ctx, "ninja")
	if err != nil {
		return plan, fmt.Errorf("resolve Ninja: %w", err)
	}
	plan.ninja = ninja
	log.Line("tool resolved: %s", ninja)

	sink.emit(ProgressEvent{Stage: StageResolveSkia, Message: "Discovering Skia requirement from upstream data"})
	plat := aseprite.SkiaPlatform{
		Name: skiaOSName(info.OS),
		CPU:  skiaCPUFor(info.Arch),
	}
	var req aseprite.SkiaRequirement
	if s.skiaResolutionNeedsBashFree(info.OS) {
		// Windows: laf/misc/skia-url.sh is bash; derive the URL from
		// the script's own template instead of executing it.
		req, err = aseprite.DiscoverSkiaRequirementBashFree(provisioned.SourceDir, plat)
	} else {
		req, err = aseprite.DiscoverSkiaRequirement(ctx, s.opts.Runner, provisioned.SourceDir, plat)
	}
	if err != nil {
		return plan, fmt.Errorf("discover Skia requirement: %w", err)
	}
	log.Line("Skia requirement: tag=%s cpu=%s", req.Tag, req.CPU)

	skiaMgr := dependencies.SkiaManager{
		Layout:    s.opts.Layout,
		Client:    s.upstreamClient(),
		UserAgent: "aseprite-builder/" + s.opts.BuilderVersion,
		Log:       log.Line,
	}
	skiaRes, err := skiaMgr.Ensure(ctx, dependencies.SkiaRequest{
		Tag:  req.Tag,
		URL:  req.URL,
		CPU:  req.CPU,
		Name: skiaAssetName(req.URL),
	})
	if err != nil {
		return plan, fmt.Errorf("resolve Skia: %w", err)
	}
	log.Line("Skia resolved at %s (reused=%v)", skiaRes.Dir, skiaRes.Reused)
	plan.skiaDir = skiaRes.Dir
	return plan, nil
}

func (s *Service) upstreamClient() *upstream.Client {
	if s.opts.Upstream != nil {
		return s.opts.Upstream
	}
	return upstream.New()
}

// pruneOldSources removes retained source trees other than the newest
// generation. Only directories the builder itself created (names
// aseprite-*) are considered; anything else is left untouched, as the
// uninstall rules require.
func (s *Service) pruneOldSources(keep string, log *logging.Logger) error {
	return s.pruneGenerations(s.opts.Layout.Sources, keep, log)
}

// pruneGenerations retains:
//   - the active directory, and
//   - the previously installed version's workspace (from install state),
//
// and removes anything else under dir that matches the aseprite-*
// naming this tool uses. Unknown directories are never touched.
func (s *Service) pruneGenerations(dir, keep string, log *logging.Logger) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	inst, err := s.opts.Store.LoadInstall()
	prevWorkspace := ""
	if err == nil && inst.WorkspacePath != "" {
		prevWorkspace = filepath.Base(inst.WorkspacePath)
	}

	var gens []gen
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "aseprite-") {
			continue // not builder-owned naming
		}
		full := filepath.Join(dir, e.Name())
		if full == keep || e.Name() == prevWorkspace {
			continue
		}
		gens = append(gens, gen{name: e.Name(), path: full})
	}
	// Sort by embedded version (aseprite-1.3.18.5), newest first.
	sort.Slice(gens, func(i, j int) bool {
		vi, _ := version.Parse(strings.TrimPrefix(gens[i].name, "aseprite-"))
		vj, _ := version.Parse(strings.TrimPrefix(gens[j].name, "aseprite-"))
		return vj.Less(vi)
	})
	for i, g := range gens {
		if i < maxRetainedGenerations {
			continue
		}
		log.Line("pruning old workspace %s", g.path)
		if err := os.RemoveAll(g.path); err != nil {
			log.Line("note: pruning %s failed: %v", g.path, err)
		}
	}
	return nil
}

const maxRetainedGenerations = 1

type gen struct{ name, path string }
