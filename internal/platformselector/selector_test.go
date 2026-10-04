package platformselector

import "testing"

// Optional capabilities the application service consumes by type
// assertion. Each is a pointer-receiver method, so the selector MUST
// store a pointer in the platform.Platform interface — a value would
// make every assertion fail silently.
type unownedReplacer interface{ SetAllowReplaceUnowned(bool) }

type installRootSetter interface{ SetInstallRoot(string) error }

type retentionSetter interface{ SetRetainPrevious(bool) }

func TestSelectedPlatformSupportsOptionalCapabilities(t *testing.T) {
	p, err := NewForRunningSystem("test")
	if err != nil {
		t.Fatalf("NewForRunningSystem: %v", err)
	}
	if _, ok := p.(unownedReplacer); !ok {
		t.Errorf("selected platform %T must support SetAllowReplaceUnowned (pointer wiring)", p)
	}
	if _, ok := p.(installRootSetter); !ok {
		t.Errorf("selected platform %T must support SetInstallRoot", p)
	}
	if _, ok := p.(retentionSetter); !ok {
		t.Errorf("selected platform %T must support SetRetainPrevious", p)
	}
}
