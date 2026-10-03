package version

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in    string
		parts []int
		raw   string
	}{
		{"1.3.18.6", []int{1, 3, 18, 6}, "1.3.18.6"},
		{"v1.3.18.6", []int{1, 3, 18, 6}, "1.3.18.6"}, // canonical form strips the "v" prefix
		{"1.3", []int{1, 3}, "1.3"},
		{"  1.2  ", []int{1, 2}, "1.2"},
		{"4.1.2", []int{4, 1, 2}, "4.1.2"},
	}
	for _, tc := range tests {
		v, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if len(v.Parts) != len(tc.parts) {
			t.Fatalf("Parse(%q) parts = %v want %v", tc.in, v.Parts, tc.parts)
		}
		for i := range tc.parts {
			if v.Parts[i] != tc.parts[i] {
				t.Errorf("Parse(%q) parts[%d] = %d want %d", tc.in, i, v.Parts[i], tc.parts[i])
			}
		}
		if v.Raw != tc.raw {
			t.Errorf("Parse(%q) raw = %q want %q", tc.in, v.Raw, tc.raw)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "   ", "abc", "1.3.4-beta", "v1.x", "1.3.4-rc1"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.3.18.6", "1.3.18.5", 1},
		{"1.3.18.6", "1.3.18.6", 0},
		{"1.3.18.6", "1.3.19.0", -1},
		{"1.3.18", "1.3.18.0", 0},
		{"1.3.18.6", "1.3.18.10", -1},
		{"1.3", "1.3.0.1", -1},
		{"2.0", "1.9.9.9", 1},
		{"3.20", "3.19.2", 1},
	}
	for _, tc := range tests {
		a, _ := Parse(tc.a)
		b, _ := Parse(tc.b)
		if got := a.Compare(b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLess(t *testing.T) {
	a, _ := Parse("1.2.3")
	b, _ := Parse("2.0.0")
	if !a.Less(b) || b.Less(a) {
		t.Error("Less relationship broken")
	}
}

func TestAtLeast(t *testing.T) {
	cmake, _ := Parse("3.20")
	tests := []struct {
		got, min string
		want     bool
	}{
		{"3.31.6", "3.20", true},
		{"3.20", "3.20", true},
		{"3.19.2", "3.20", false},
		{"4.0", "3.20", true},
		{"2.9", "3.20", false},
	}
	for _, tc := range tests {
		v, err := Parse(tc.got)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.got, err)
		}
		if got := v.AtLeast(cmake); got != tc.want {
			t.Errorf("AtLeast(%q) = %v want %v", tc.got, got, tc.want)
		}
	}
}

func TestIsZero(t *testing.T) {
	if !(Version{}).IsZero() {
		t.Error("empty version should be zero")
	}
	v, err := Parse("1.3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v.IsZero() {
		t.Error("1.3 should not be zero")
	}
}

func TestString(t *testing.T) {
	v, _ := Parse("1.3.18.6")
	if v.String() != "1.3.18.6" {
		t.Errorf("String = %q", v.String())
	}
	empty := Version{}
	if got, want := empty.String(), ""; got != want {
		t.Errorf("zero String = %q, want %q", got, want)
	}
	// Parts-only rendering when Raw is unset.
	v2 := Version{Parts: []int{1, 2}}
	if got, want := v2.String(), "1.2"; got != want {
		t.Errorf("parts String = %q, want %q", got, want)
	}
}
