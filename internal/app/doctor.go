package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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
	Installation *InstallationDoctor  `json:"installation,omitempty"`
	Status       string               `json:"status"` // "healthy" | "issues"
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

	// Tool resolution (no downloads performed here)
	resolver := &dependencies.Resolver{
		Runner: s.opts.Runner,
		Layout: s.opts.Layout,
		Fetchers: map[string]dependencies.Fetcher{
			"cmake": dependencies.CMakeFetcher{Layout: s.opts.Layout},
			"ninja": dependencies.NinjaFetcher{Layout: s.opts.Layout},
		},
		MinVersions: map[string]version.Version{
			"cmake": minCForDoctor(),
			"ninja": {}, // any version
		},
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
	}

	report.Status = report.overallStatus()
	return report, nil
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
	if r.Installation != nil && r.Installation.HealthyChecked && !r.Installation.Healthy {
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
	if r.Installation != nil && r.Installation.HealthyChecked {
		if r.Installation.Healthy {
			rowOK("Installation", fmt.Sprintf("Aseprite %s at %s", r.Installation.InstalledVersion, r.Installation.AppPath))
		} else {
			rowFail("Installation", "broken — run: aseprite-builder repair")
		}
	} else {
		rowOK("Installation", "none (nothing installed yet)")
	}
	return rows
}
