package aseprite

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	v, err := ParseVersion("v1.3.18.6")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if got, want := v.String(), "1.3.18.6"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestParseVersionErrors(t *testing.T) {
	for _, in := range []string{"", "beta2", "1.3.4-beta"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) should fail", in)
		}
	}
}

func TestSourceAssetName(t *testing.T) {
	r := Release{TagName: "v1.3.18.6"}
	if got, want := r.SourceAssetName(), "Aseprite-v1.3.18.6-Source.zip"; got != want {
		t.Errorf("SourceAssetName = %q, want %q", got, want)
	}
}

func TestStable(t *testing.T) {
	if !(Release{TagName: "v1"}).Stable() {
		t.Error("plain release should be stable")
	}
	if (Release{TagName: "v1", PreRelease: true}).Stable() {
		t.Error("pre-release should not be stable")
	}
	if (Release{TagName: "v1", Draft: true}).Stable() {
		t.Error("draft should not be stable")
	}
}

func TestReleaseString(t *testing.T) {
	v, _ := ParseVersion("1.3.18.6")
	r := Release{Version: v, TagName: "v1.3.18.6"}
	if got := r.String(); !strings.Contains(got, "1.3.18.6") {
		t.Errorf("String = %q", got)
	}
}
