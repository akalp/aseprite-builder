package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/dependencies"
	"github.com/akalp/aseprite-builder/internal/logging"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/state"
	"github.com/akalp/aseprite-builder/internal/version"
)

// DoctorReport is the full diagnostics payload; the JSON produced by
// `doctor --json` mirrors this structure.
type DoctorReport struct {
	GeneratedAt  time.Time            `json:"generated_at"`
	System       platform.SystemInfo  `json:"system"`
	Requirements []platform.Check     `json:"requirements"`
	Tools        []dependencies.Check `json:"tools"`
	Skia         SkiaDoctor           `json:"skia"`
	Contract     *ContractDoctor      `json:"contract,omitempty"`
	Installation *InstallationDoctor  `json:"installation,omitempty"`
	Integration  *IntegrationDoctor   `json:"integration,omitempty"`
	Status       string               `json:"status"` // "healthy" | "issues"
}

// ContractDoctor summarizes the build contract of a cached source tree.
type ContractDoctor struct {
	Source      string `json:"source,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Recognized  bool   `json:"recognized"`
}

// IntegrationDoctor summarizes user-local integration health (Linux
// CLI symlink / desktop entry / icons). Present only when the platform
// can validate its integration and an installation exists.
type IntegrationDoctor struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// SkiaDoctor summarizes the Skia artifact cache.
type SkiaDoctor struct {
	CachedTags []string `json:"cached_tags,omitempty"`
	Status     string   `json:"status"` // "none-cached" | "cached"
}

// InstallationDoctor summarizes the installed state.
type InstallationDoctor struct {
	InstalledVersion string    `json:"installed_version,omitempty"`
	AppPath          string    `json:"app_path,omitempty"`
	InstalledAt      time.Time `json:"installed_at,omitempty"`
	Healthy          bool      `json:"healthy,omitempty"`
	HealthyChecked   bool      `json:"healthy_checked"` // false when no install exists
}

// Doctor runs every diagnostic check that can run without installing
// anything.
func (s *Service) Doctor(ctx context.Context) (DoctorReport, error) {
	report := DoctorReport{GeneratedAt: time.Now().UTC()}

	// System
	info, err := s.opts.Platform.Detect(ctx)
	if err != nil {
		return report, err
	}
	report.System = info

	// System-managed requirements
	checks, err := s.opts.Platform.Requirements(ctx)
	if err != nil {
		return report, err
	}
	report.Requirements = checks

	// Tool resolution (no downloads performed here). The fetcher
	// selection mirrors the install flow: Windows and Linux resolve
	// their own distributions.
	var cmakeFetcher dependencies.Fetcher = dependencies.CMakeFetcher{Layout: s.opts.Layout}
	var ninjaFetcher dependencies.Fetcher = dependencies.NinjaFetcher{Layout: s.opts.Layout}
	switch report.System.OS {
	case "windows":
		cmakeFetcher = dependencies.NewWindowsCMakeFetcher(s.opts.Layout, nil)
		ninjaFetcher = dependencies.NewWindowsNinjaFetcher(s.opts.Layout, nil)
	case "linux":
		cmakeFetcher = dependencies.NewLinuxCMakeFetcher(s.opts.Layout, nil)
		ninjaFetcher = dependencies.NewLinuxNinjaFetcher(s.opts.Layout, nil)
	}
	cfg := s.runConfig()
	resolver := &dependencies.Resolver{
		Runner: s.opts.Runner,
		Layout: s.opts.Layout,
		Fetchers: map[string]dependencies.Fetcher{
			"cmake": cmakeFetcher,
			"ninja": ninjaFetcher,
		},
		MinVersions: map[string]version.Version{
			"cmake": minCForDoctor(),
			"ninja": {}, // any version
		},
		// Report the same reuse order install would take.
		PreferCached: normalizeToolPreference(cfg.ToolPreference) == "cached",
	}
	for _, name := range []string{"cmake", "ninja"} {
		c, err := resolver.Check(ctx, name)
		if err != nil {
			c = dependencies.Check{Name: name, Status: "error"}
		}
		report.Tools = append(report.Tools, c)
	}

	// Skia cache
	report.Skia = s.skiaDoctor()

	// Cached source build contract: re-inspected cheaply, exactly like
	// the install pipeline would (spec §28).
	report.Contract = s.contractDoctor(info)

	// Installation
	inst, hasInstall, err := s.CurrentInstall()
	switch {
	case err != nil:
		return report, err
	case hasInstall:
		healthy := s.installHealthyQuiet(ctx, inst)
		report.Installation = &InstallationDoctor{
			InstalledVersion: inst.AsepriteVersion,
			AppPath:          inst.AppPath,
			InstalledAt:      inst.InstalledAt,
			Healthy:          healthy,
			HealthyChecked:   true,
		}
		if checker, ok := s.opts.Platform.(platform.IntegrationDoctor); ok {
			integOK, detail := checker.CheckIntegration()
			report.Integration = &IntegrationDoctor{OK: integOK, Detail: detail}
		}
	}

	report.Status = report.overallStatus()
	return report, nil
}

// contractDoctor inspects a cached source tree the way the pipeline
// would: the installed workspace first, then the newest builder-owned
// source. No cached source means no row (the contract is verified at
// install time).
func (s *Service) contractDoctor(info platform.SystemInfo) *ContractDoctor {
	dir := ""
	if inst, has, err := s.CurrentInstall(); err == nil && has && inst.WorkspacePath != "" {
		if _, statErr := os.Stat(filepath.Join(inst.WorkspacePath, "CMakeLists.txt")); statErr == nil {
			dir = inst.WorkspacePath
		}
	}
	if dir == "" {
		dir = s.newestSourceTree()
	}
	if dir == "" {
		return nil
	}
	inspector := aseprite.NewInspector()
	contract, err := inspector.Inspect(dir, outputConfigFor(info.OS))
	if err != nil {
		return &ContractDoctor{Source: dir, Recognized: false}
	}
	return &ContractDoctor{Source: dir, Fingerprint: contract.ContractFingerprint(), Recognized: true}
}

// newestSourceTree returns the newest builder-owned aseprite-* source
// tree carrying a CMakeLists.txt, or "".
func (s *Service) newestSourceTree() string {
	entries, err := os.ReadDir(s.opts.Layout.Sources)
	if err != nil {
		return ""
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "aseprite-") {
			continue
		}
		dir := filepath.Join(s.opts.Layout.Sources, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "CMakeLists.txt")); err == nil {
			dirs = append(dirs, dir)
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		vi := strings.TrimPrefix(filepath.Base(dirs[i]), "aseprite-")
		vj := strings.TrimPrefix(filepath.Base(dirs[j]), "aseprite-")
		vi = strings.TrimPrefix(vi, "dev-")
		vj = strings.TrimPrefix(vj, "dev-")
		return vi > vj // newest first by lexical version-ish order
	})
	if len(dirs) == 0 {
		return ""
	}
	return dirs[0]
}

// minCForDoctor reports the *minimum* constraint as "any" when no
// source has been inspected yet; the actual minimum is discovered from
// the Aseprite source at install time. Doctor therefore reports
// compatibility against 0.0.0 (presence-only).
func minCForDoctor() version.Version { return version.Version{} }

// installHealthyQuiet is a non-fatal health check wrapper.
func (s *Service) installHealthyQuiet(ctx context.Context, inst state.InstallState) bool {
	log := logging.NewNop()
	return s.installationHealthy(ctx, inst, log)
}

func (s *Service) skiaDoctor() SkiaDoctor {
	entries, err := os.ReadDir(s.opts.Layout.Skia)
	if err != nil || len(entries) == 0 {
		return SkiaDoctor{Status: "none-cached"}
	}
	var tags []string
	for _, e := range entries {
		if e.IsDir() {
			tags = append(tags, e.Name())
		}
	}
	if len(tags) == 0 {
		return SkiaDoctor{Status: "none-cached"}
	}
	return SkiaDoctor{Status: "cached", CachedTags: tags}
}

// overallStatus computes the summary string.
func (r DoctorReport) overallStatus() string {
	for _, c := range r.Requirements {
		if !c.OK {
			return "issues"
		}
	}
	for _, t := range r.Tools {
		if t.Status == "none" || t.Status == "error" {
			return "issues"
		}
	}
	if r.Contract != nil && !r.Contract.Recognized {
		return "issues"
	}
	if r.Installation != nil && r.Installation.HealthyChecked && !r.Installation.Healthy {
		return "issues"
	}
	if r.Integration != nil && !r.Integration.OK {
		return "issues"
	}
	return "healthy"
}

// DoctorRow is one diagnostic check as rendered by both frontends: the
// CLI's `doctor` output and the TUI diagnostics screen show the same
// facts, built by the same code.
type DoctorRow struct {
	// OK distinguishes ✓ from ✗ rows.
	OK bool
	// Name is the left column (e.g. "Operating system").
	Name string
	// Detail is the right column (status, version, fix hint...).
	Detail string
}

// DoctorRows builds the diagnostic rows for a report. This is the
// single source of the doctor table; no per-frontend duplication.
func (r DoctorReport) DoctorRows() []DoctorRow {
	var rows []DoctorRow
	rowOK := func(name, detail string) { rows = append(rows, DoctorRow{OK: true, Name: name, Detail: detail}) }
	rowFail := func(name, detail string) { rows = append(rows, DoctorRow{OK: false, Name: name, Detail: detail}) }

	rowOK("Operating system", fmt.Sprintf("%s %s", r.System.OS, r.System.OSVersion))
	if r.System.Supported {
		rowOK("Architecture", r.System.Arch)
	} else {
		rowFail("Architecture", r.System.Arch+" (unsupported)")
	}
	for _, c := range r.Requirements {
		if c.OK {
			rowOK(c.Name, c.Detail)
		} else {
			rowFail(c.Name, c.Fix)
		}
	}
	for _, t := range r.Tools {
		if t.Status == "none" || t.Status == "error" {
			rowFail(t.Name, "unavailable and no downloadable alternative was configured")
		} else {
			rowOK(t.Name, t.Display())
		}
	}
	if r.Skia.Status == "cached" {
		rowOK("Skia", "cached: "+strings.Join(r.Skia.CachedTags, ", "))
	} else {
		rowOK("Skia", "no cached artifact (resolved during install)")
	}
	if r.Contract != nil {
		if r.Contract.Recognized {
			rowOK("Build contract", "recognized ("+r.Contract.Fingerprint+")")
		} else {
			rowFail("Build contract", "cached source at "+r.Contract.Source+" is not recognized — run repair or reinstall")
		}
	}
	if r.Installation != nil && r.Installation.HealthyChecked {
		if r.Installation.Healthy {
			rowOK("Installation", fmt.Sprintf("Aseprite %s at %s", r.Installation.InstalledVersion, r.Installation.AppPath))
		} else {
			rowFail("Installation", "broken — run: aseprite-builder repair")
		}
	} else {
		rowOK("Installation", "none (nothing installed yet)")
	}
	if r.Integration != nil {
		if r.Integration.OK {
			rowOK("CLI integration", r.Integration.Detail)
		} else {
			rowFail("CLI integration", r.Integration.Detail)
		}
	}
	return rows
}
