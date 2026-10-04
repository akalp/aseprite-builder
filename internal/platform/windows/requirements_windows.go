//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/runner"
)

// Detection contract facts, mirrored from upstream's own build.cmd:
// vswhere lives at %ProgramFiles(x86)%\Microsoft Visual Studio\
// Installer\vswhere.exe and the required workload component is the
// x86/x64 C++ tools. These are Microsoft's own stable identifiers, not
// version pins.
const (
	vswhereRelPath   = `Microsoft Visual Studio\Installer\vswhere.exe`
	vcToolsComponent = "Microsoft.VisualStudio.Component.VC.Tools.x86.x64"
	// vcvarsRelPath is the environment loader inside a VC tools
	// installation (upstream build.cmd: -find VC\Auxiliary\Build\vcvars64.bat).
	vcvarsRelPath = `VC\Auxiliary\Build\vcvars64.bat`
)

// windowsKitsInclude is the fixed system location of the Windows SDK
// headers (%ProgramFiles(x86)%\Windows Kits\10\Include\<version>).
const windowsKitsInclude = `Windows Kits\10\Include`

// programFilesX86 resolves %ProgramFiles(x86)% with the WOW64
// redirection-aware default.
func programFilesX86() string {
	if v := os.Getenv("ProgramFiles(x86)"); v != "" {
		return v
	}
	return `C:\Program Files (x86)`
}

// vswherePath is the system location of vswhere.exe (installed by any
// Visual Studio / Build Tools product; never shipped by us).
func vswherePath() string {
	return filepath.Join(programFilesX86(), vswhereRelPath)
}

// Requirements checks the system-managed Microsoft toolchain plus free
// disk space. Everything here is detection only: nothing is installed,
// unpacked or modified (spec §16/§21).
func (p Platform) Requirements(ctx context.Context) ([]platform.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var checks []platform.Check

	// 1. Visual Studio / Build Tools with the C++ workload (vswhere).
	vswhere := vswherePath()
	if _, err := os.Stat(vswhere); err != nil {
		checks = append(checks, platform.Check{
			Name:   "Visual Studio Build Tools (C++ workload)",
			OK:     false,
			Detail: "vswhere.exe not found — no Visual Studio or Build Tools installation detected",
			Fix: "Install Visual Studio Build Tools with the 'Desktop development with C++' workload: " +
				"https://visualstudio.microsoft.com/downloads/",
		})
	} else {
		res, err := p.Runner.Run(ctx, runner.Command{
			Name: vswhere,
			Args: []string{
				"-latest", "-products", "*",
				"-requires", vcToolsComponent,
				"-property", "installationPath",
			},
		})
		if err == nil && res.OK() && strings.TrimSpace(res.Stdout) != "" {
			path := strings.TrimSpace(strings.SplitN(res.Stdout, "\n", 2)[0])
			checks = append(checks, platform.Check{
				Name:   "Visual Studio Build Tools (C++ workload)",
				OK:     true,
				Detail: path,
				Source: "system",
			})
		} else {
			checks = append(checks, platform.Check{
				Name:   "Visual Studio Build Tools (C++ workload)",
				OK:     false,
				Detail: "VS is installed but the 'Desktop development with C++' workload is missing",
				Fix:    "Re-run the Visual Studio Installer and add the 'Desktop development with C++' workload",
			})
		}
	}

	// 2. vcvars64.bat (the build environment loader).
	vcvars, vcvarsErr := p.findVCVars(ctx)
	switch {
	case vcvarsErr == nil:
		checks = append(checks, platform.Check{
			Name:   "MSVC environment (vcvars64.bat)",
			OK:     true,
			Detail: vcvars,
			Source: "system",
		})
	default:
		checks = append(checks, platform.Check{
			Name: "MSVC environment (vcvars64.bat)",
			OK:   false,
			Fix:  "Install the MSVC x86/x64 build tools component (included in the C++ workload)",
		})
	}

	// 3. Windows SDK (detection: newest SDK with um/windows.h).
	sdk, sdkErr := newestWindowsSDK(programFilesX86())
	switch {
	case sdkErr == nil:
		checks = append(checks, platform.Check{
			Name:   "Windows SDK",
			OK:     true,
			Detail: sdk,
			Source: "system",
		})
	default:
		checks = append(checks, platform.Check{
			Name: "Windows SDK",
			OK:   false,
			Fix:  "Install the Windows 10/11 SDK from the Visual Studio Installer (component: Microsoft.Windows.SDK)",
		})
	}

	// 4. Disk space (non-fatal warning level).
	free, err := freeDiskBytes(p.Runner, p.ProgramsRoot)
	switch {
	case err != nil:
		checks = append(checks, platform.Check{Name: "Disk space", OK: false, Fix: "Check free space on your drive"})
	case free < minFreeDiskBytes:
		checks = append(checks, platform.Check{
			Name:   "Disk space",
			OK:     false,
			Detail: fmt.Sprintf("%.1f GiB free, at least 10 GiB needed", float64(free)/(1<<30)),
		})
	default:
		checks = append(checks, platform.Check{
			Name:   "Disk space",
			OK:     true,
			Detail: fmt.Sprintf("%.1f GiB free", float64(free)/(1<<30)),
		})
	}

	return checks, nil
}

// findVCVars locates vcvars64.bat via vswhere's -find (the exact
// upstream build.cmd invocation). Returns an error when absent.
func (p Platform) findVCVars(ctx context.Context) (string, error) {
	vswhere := vswherePath()
	if _, err := os.Stat(vswhere); err != nil {
		return "", fmt.Errorf("vswhere not available: %w", err)
	}
	res, err := p.Runner.Run(ctx, runner.Command{
		Name: vswhere,
		Args: []string{
			"-latest", "-products", "*",
			"-requires", vcToolsComponent,
			"-find", vcvarsRelPath,
		},
	})
	if err != nil {
		return "", err
	}
	if !res.OK() {
		return "", fmt.Errorf("vswhere -find exited with code %d", res.ExitCode)
	}
	line := strings.TrimSpace(strings.SplitN(res.Stdout, "\n", 2)[0])
	if line == "" {
		return "", errors.New("vswhere found no vcvars64.bat")
	}
	return line, nil
}

// sdkVersionRe parses "10.0.26100.0" directory names; superfluous
// trailing segments are tolerated ("10.0.22000.51").
var sdkVersionRe = regexp.MustCompile(`^10\.0\.\d+(\.\d+)?$`)

// newestWindowsSDK probes the fixed Windows Kits include root and
// returns the highest installed SDK version directory containing the
// UM headers.
func newestWindowsSDK(progFilesX86 string) (string, error) {
	root := filepath.Join(progFilesX86, windowsKitsInclude)
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("Windows Kits include dir %s: %w", root, err)
	}
	var versions []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !sdkVersionRe.MatchString(e.Name()) {
			continue
		}
		// A usable SDK has um\windows.h.
		if _, err := os.Stat(filepath.Join(root, e.Name(), "um", "windows.h")); err == nil {
			versions = append(versions, e.Name())
		}
	}
	if len(versions) == 0 {
		return "", errors.New("no Windows SDK with UM headers found")
	}
	sort.Slice(versions, func(i, j int) bool {
		return sdkVersionLess(versions[j], versions[i]) // newest first
	})
	return versions[0], nil
}

// sdkVersionLess orders SDK version dirs numerically by segment.
func sdkVersionLess(a, b string) bool {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, bi := segmentInt(as[i]), segmentInt(bs[i])
		if ai != bi {
			return ai < bi
		}
	}
	return a < b
}

func segmentInt(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// BuildEnvironment captures the Visual Studio toolchain environment by
// importing vcvars64.bat's `set` output. The captured entries are
// passed as the build's explicit environment so cmake/ninja/cl run
// with the MSVC compiler, INCLUDE/LIB paths and SDK variables in place
// (exactly what upstream build.cmd achieves by calling vcvars64).
func (p Platform) BuildEnvironment(ctx context.Context) ([]string, error) {
	vcvars, err := p.findVCVars(ctx)
	if err != nil {
		return nil, fmt.Errorf("locate vcvars64.bat: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	res, err := p.Runner.Run(ctx, runner.Command{
		Name: "cmd",
		Args: []string{"/d", "/c", "call", vcvars, "&&", "set"},
	})
	if err != nil {
		return nil, fmt.Errorf("import MSVC environment: %w", err)
	}
	if !res.OK() {
		return nil, fmt.Errorf("importing the MSVC environment exited with code %d: %s",
			res.ExitCode, lastLine(res.Stderr))
	}
	env := parseEnvBlock(res.Stdout)
	if !envContainsPrefix(env, "PATH=") {
		return nil, fmt.Errorf("the MSVC environment has no PATH; vcvars output looked wrong")
	}
	return env, nil
}

// parseEnvBlock turns `set` output ("KEY=VALUE" lines, possibly
// CRLF-terminated) into Command.Env entries, skipping lines that are
// not assignments (e.g. the echo of the `set` command itself).
func parseEnvBlock(out string) []string {
	var env []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "=") {
			// Dynamic session vars like "=C:" are not environment entries.
			continue
		}
		env = append(env, line)
	}
	return env
}

func envContainsPrefix(env []string, prefix string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Prepare currently performs no Windows-specific setup: everything the
// build needs is validated by Requirements and captured per-build in
// BuildEnvironment.
func (p Platform) Prepare(context.Context) error { return nil }
