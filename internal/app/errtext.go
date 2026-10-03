package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/akalp/aseprite-builder/internal/aseprite"
)

// FriendlyMessage maps typed service errors to the plain-language
// failure text required by the spec (§39: never raw build output as
// the primary message). The CLI and the TUI both render it verbatim,
// so the two frontends cannot drift.
func FriendlyMessage(err error) string {
	switch {
	case errors.Is(err, ErrSystemRequirements):
		var sre SystemRequirementsError
		ok := errors.As(err, &sre)
		var b strings.Builder
		b.WriteString("This machine is missing some requirements before Aseprite can be built.\n")
		if ok {
			for _, ck := range sre.Checks {
				if ck.OK {
					continue
				}
				fmt.Fprintf(&b, "  ✗ %s\n", ck.Name)
				if ck.Fix != "" {
					fmt.Fprintf(&b, "    → %s\n", ck.Fix)
				}
			}
		}
		b.WriteString("Nothing was installed or modified.")
		return b.String()
	case errors.Is(err, ErrUnknownContract):
		var uce UnknownContractError
		detail := ""
		if errors.As(err, &uce) && uce.Reason != nil {
			detail = "\n\nDetails: " + uce.Reason.Error()
		}
		return "This Aseprite release uses a build layout that this version of\n" +
			"aseprite-builder does not recognize. To stay safe, nothing was built or\n" +
			"installed. Updating aseprite-builder may add support." + detail
	case errors.Is(err, ErrUpToDate):
		return "Aseprite is already up to date."
	case errors.Is(err, ErrAlreadyInstalled):
		return "This Aseprite version is already installed."
	case errors.Is(err, ErrNotInstalled):
		return "This command needs an aseprite-builder installation (run install first)."
	default:
		var buildErr *aseprite.UpstreamBuildError
		if errors.As(err, &buildErr) {
			return fmt.Sprintf("Aseprite could not be compiled: the upstream build process failed.\n"+
				"Check the operation log for the exact compiler output.%s", brief(buildErr.Tail, 1))
		}
		var contract *aseprite.ContractViolationError
		if errors.As(err, &contract) {
			return "The upstream build did not produce the expected output. This usually means\n" +
				"the Aseprite build contract changed; nothing was installed.\n\n" +
				fmt.Sprintf("Details: %v", contract)
		}
		msg := err.Error()
		if unpack := friendlyDownloadError(err); unpack != "" {
			return unpack
		}
		if strings.Contains(msg, "context canceled") || strings.Contains(msg, "context deadline exceeded") {
			return "Operation cancelled."
		}
		return firstLine(msg)
	}
}

// friendlyDownloadError turns network failures into actionable text.
func friendlyDownloadError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "connection refused"):
		return "aseprite-builder could not reach the internet. Check your network connection\nand try again."
	case strings.Contains(msg, "rate limited"):
		return "GitHub's API rate limit was hit. Aseprite metadata could not be resolved\nright now; try again later."
	case strings.Contains(msg, "sha256 mismatch"):
		return "A downloaded archive failed verification and was discarded.\nNothing was installed. Please try again."
	case strings.Contains(msg, "unexpected HTTP status 404"):
		return "The requested upstream artifact was not found; the upstream release page\nmay have changed."
	default:
		return ""
	}
}

// brief extracts n lines as an indented detail block.
func brief(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, "\n")
	if out == "" {
		return ""
	}
	return "\n\n   " + strings.Join(lines, "\n   ")
}

func firstLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	out := lines[0]
	if len(lines) > 1 {
		out += " (more details in the operation log)"
	}
	return out
}
