package packages

import (
	"reflect"
	"testing"
)

func TestParseOSReleaseUbuntu(t *testing.T) {
	rel := ParseOSRelease(`
PRETTY_NAME="Ubuntu 24.04.1 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
VERSION="24.04.1 LTS (Noble Numbat)"
VERSION_CODENAME=noble
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
# a comment
`)
	if rel.ID != "ubuntu" {
		t.Errorf("ID = %q", rel.ID)
	}
	if !reflect.DeepEqual(rel.IDLike, []string{"debian"}) {
		t.Errorf("IDLike = %v", rel.IDLike)
	}
	if rel.VersionID != "24.04" || rel.VersionCodename != "noble" {
		t.Errorf("version fields = %q / %q", rel.VersionID, rel.VersionCodename)
	}
	if rel.PrettyName != "Ubuntu 24.04.1 LTS" {
		t.Errorf("PrettyName = %q", rel.PrettyName)
	}
	if !rel.IsDebianLike() {
		t.Error("ubuntu must be debian-like")
	}
}

func TestParseOSReleaseDebianDerivative(t *testing.T) {
	rel := ParseOSRelease("ID=linuxmint\nID_LIKE=\"ubuntu debian\"\nVERSION_ID=21.3\n")
	if !rel.IsDebianLike() {
		t.Error("linux mint (ID_LIKE=ubuntu debian) must be debian-like")
	}
}

func TestParseOSReleaseQuotingAndGarbage(t *testing.T) {
	rel := ParseOSRelease(`
ID='fedora'
ID_LIKE="rhel centos"
BROKEN LINE WITHOUT EQUALS
EMPTY=
VERSION_ID="39"
`)
	if rel.ID != "fedora" || !reflect.DeepEqual(rel.IDLike, []string{"rhel", "centos"}) {
		t.Errorf("rel = %+v", rel)
	}
	if rel.VersionID != "39" {
		t.Errorf("VersionID = %q", rel.VersionID)
	}
	if rel.IsDebianLike() {
		t.Error("fedora must not be debian-like")
	}
}

func TestParseOSReleaseEmpty(t *testing.T) {
	rel := ParseOSRelease("")
	if rel.IsDebianLike() {
		t.Error("empty release must not claim a family")
	}
	if rel.Display() != "unknown" {
		t.Errorf("Display() = %q", rel.Display())
	}
}

func TestOSReleaseDisplayFallbacks(t *testing.T) {
	cases := []struct {
		rel  OSRelease
		want string
	}{
		{OSRelease{PrettyName: "Debian GNU/Linux 12 (bookworm)"}, "Debian GNU/Linux 12 (bookworm)"},
		{OSRelease{ID: "ubuntu", VersionID: "24.04"}, "ubuntu 24.04"},
		{OSRelease{ID: "arch"}, "arch"},
		{OSRelease{}, "unknown"},
	}
	for _, c := range cases {
		if got := c.rel.Display(); got != c.want {
			t.Errorf("Display(%+v) = %q, want %q", c.rel, got, c.want)
		}
	}
}
