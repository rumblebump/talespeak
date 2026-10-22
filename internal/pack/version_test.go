package pack

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for _, s := range []string{"0.0.0", "1.2.3", "10.20.30", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-0.3.7", "1.0.0-x.7.z.92", "1.0.0-x-y-z.--", "999999999.999999999.999999999"} {
		v, err := ParseVersion(s)
		if err != nil || v.String() != s {
			t.Errorf("ParseVersion(%q) = %v, %v", s, v, err)
		}
	}
	for _, s := range []string{"", "1", "1.2", "v1.2.3", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "1.2.3-", "1.2.3-01", "1.2.3-a..b", "1.2.3+build", "1.2.3 ", " 1.2.3",
		"-1.2.3", "1.2.3-é", "18446744073709551616.0.0", "1000000000.0.0", "1.2.3-" + strings.Repeat("a", 60)} {
		if v, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want an error", s, v)
		}
	}
}

// The order of semver.org's precedence example, and pre-releases before
// their release.
func TestVersionCompare(t *testing.T) {
	order := []string{"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.0.1", "1.1.0", "1.10.0", "2.0.0"}
	for i, a := range order {
		va, _ := ParseVersion(a)
		for j, b := range order {
			vb, _ := ParseVersion(b)
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := va.Compare(vb); got != want {
				t.Errorf("%s vs %s = %d, want %d", a, b, got, want)
			}
		}
	}
}

func TestRange(t *testing.T) {
	for _, tt := range []struct {
		r       string
		in, out []string
	}{
		{"1.2.3", []string{"1.2.3"}, []string{"1.2.4", "1.2.3-beta"}},
		{"=1.2.3", []string{"1.2.3"}, []string{"1.2.2"}},
		{"^1.2", []string{"1.2.0", "1.9.9", "1.3.0-beta"}, []string{"1.1.9", "2.0.0", "2.0.0-beta", "1.2.0-beta"}},
		{"^1.2.3", []string{"1.2.3", "1.4.0"}, []string{"1.2.2", "2.0.0"}},
		{"^1", []string{"1.0.0", "1.99.0"}, []string{"0.9.0", "2.0.0"}},
		{"^1.x", []string{"1.0.0", "1.99.0"}, []string{"2.0.0"}},
		{"^0.2.3", []string{"0.2.3", "0.2.9"}, []string{"0.3.0", "0.2.2"}},
		{"^0.2", []string{"0.2.0", "0.2.9"}, []string{"0.3.0"}},
		{"^0.0.3", []string{"0.0.3"}, []string{"0.0.4", "0.0.2"}},
		{"^0.0", []string{"0.0.0", "0.0.9"}, []string{"0.1.0"}},
		{"^0", []string{"0.0.0", "0.9.0"}, []string{"1.0.0"}},
		{"~1.2.3", []string{"1.2.3", "1.2.9"}, []string{"1.3.0", "1.2.2"}},
		{"~1.2", []string{"1.2.0", "1.2.9"}, []string{"1.3.0"}},
		{"~1", []string{"1.0.0", "1.9.0"}, []string{"2.0.0"}},
		{"1.x", []string{"1.0.0", "1.9.9"}, []string{"2.0.0", "0.9.0"}},
		{"1.2.x", []string{"1.2.0", "1.2.9"}, []string{"1.3.0"}},
		{"1.2.*", []string{"1.2.0"}, []string{"1.3.0"}},
		{"1", []string{"1.0.0", "1.5.0"}, []string{"2.0.0"}},
		{"*", []string{"0.0.0", "9.9.9", "1.0.0-beta"}, nil},
		{"x", []string{"1.0.0"}, nil},
		{">=1.0", []string{"1.0.0", "5.0.0"}, []string{"0.9.9", "1.0.0-beta"}},
		{">1.2", []string{"1.3.0", "1.3.0-beta"}, []string{"1.2.9"}},
		{">1.2.3", []string{"1.2.4"}, []string{"1.2.3"}},
		{"<2", []string{"1.9.9"}, []string{"2.0.0"}},
		{"<=1.2", []string{"1.2.9", "1.0.0"}, []string{"1.3.0"}},
		{"<=1.2.3", []string{"1.2.3"}, []string{"1.2.4"}},
		{">=1.0 <2.0", []string{"1.0.0", "1.9.9"}, []string{"2.0.0", "0.1.0"}},
		{"  >=1.0   <1.5  ", []string{"1.4.0"}, []string{"1.5.0"}},
		{"^1.0.0-beta", []string{"1.0.0-beta", "1.0.0", "1.5.0"}, []string{"1.0.0-alpha", "2.0.0"}},
	} {
		r, err := ParseRange(tt.r)
		if err != nil {
			t.Errorf("ParseRange(%q): %v", tt.r, err)
			continue
		}
		for _, s := range tt.in {
			if v, _ := ParseVersion(s); !r.Contains(v) {
				t.Errorf("%q does not contain %s", tt.r, s)
			}
		}
		for _, s := range tt.out {
			if v, _ := ParseVersion(s); r.Contains(v) {
				t.Errorf("%q contains %s", tt.r, s)
			}
		}
	}
	if (Range{}).Contains(Version{}) {
		t.Error("the zero Range holds a version")
	}
}

func TestRangeRefuses(t *testing.T) {
	for _, s := range []string{"", "   ", "^", ">=", "x.1", "1.x.2", "1.2.3.4", "01.2", "^v1", ">=*", "^*", "1.2-beta", "1.x-beta", "1.2.3-", "a", "1..2", "1.2.", "=>1",
		strings.Repeat("1 ", 70), "99999999999999999999", "^1000000000"} {
		if r, err := ParseRange(s); err == nil {
			t.Errorf("ParseRange(%q) = %v, want an error", s, r)
		}
	}
}

func TestCompatible(t *testing.T) {
	for _, tt := range []struct{ v, in, out string }{
		{"1.2.0", "1.9.0", "2.0.0"},
		{"1.2.0", "1.2.0", "1.1.9"},
		{"0.3.1", "0.3.9", "0.4.0"},
		{"0.0.5", "0.0.5", "0.0.6"},
		{"2.0.0-beta.1", "2.0.0", "3.0.0"},
	} {
		r := Compatible(mustVersion(t, tt.v))
		if !r.Contains(mustVersion(t, tt.in)) || r.Contains(mustVersion(t, tt.out)) {
			t.Errorf("Compatible(%s) = %s: want %s in, %s out", tt.v, r, tt.in, tt.out)
		}
	}
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// No range text makes the parser panic, and a parsed version prints as
// its text.
func FuzzVersions(f *testing.F) {
	for _, s := range []string{"1.2.3", "^1.2", ">=1.0 <2", "1.x", "~0.0", "1.0.0-rc.1", "*", "1.2.3-x.7.z.92"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if v, err := ParseVersion(s); err == nil {
			if v.String() != s {
				t.Errorf("ParseVersion(%q).String() = %q", s, v)
			}
			if v.Compare(v) != 0 || !Compatible(v).Contains(v) {
				t.Errorf("%q is not compatible with itself", s)
			}
		}
		if r, err := ParseRange(s); err == nil {
			r.Contains(Version{Major: 1})
		}
	})
}
