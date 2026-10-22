package pack

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Versions: a pack's version is a semantic version (semver.org),
// MAJOR.MINOR.PATCH with an optional pre-release such as 1.2.0-beta.1.
// Build metadata (+...) is not allowed, so two equal versions are always
// the same text.

// MaxVersionBytes bounds a version's text, and MaxVersionNumber each of
// its numbers, so the next version up never overflows.
const (
	MaxVersionBytes  = 64
	MaxVersionNumber = 999_999_999
)

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch uint64
	// Pre is the pre-release, without its "-"; "" for a release.
	Pre string
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*))*))?$`)

// ParseVersion reads a version like 1.2.0 or 2.0.0-rc.1.
func ParseVersion(s string) (Version, error) {
	if len(s) > MaxVersionBytes {
		return Version{}, fmt.Errorf("version is longer than %d bytes", MaxVersionBytes)
	}
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("version %q must be MAJOR.MINOR.PATCH, like 1.0.0, with an optional pre-release like 1.0.0-beta.1", s)
	}
	var v Version
	for i, p := range []*uint64{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil || n > MaxVersionNumber {
			return Version{}, fmt.Errorf("version %q: numbers go up to %d", s, MaxVersionNumber)
		}
		*p = n
	}
	v.Pre = m[4]
	return v, nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or 1 as v is older than, the same as or newer
// than w, by semver precedence: a pre-release comes before its release.
func (v Version) Compare(w Version) int {
	for _, d := range [][2]uint64{{v.Major, w.Major}, {v.Minor, w.Minor}, {v.Patch, w.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.Pre == w.Pre:
		return 0
	case v.Pre == "":
		return 1
	case w.Pre == "":
		return -1
	}
	a, b := strings.Split(v.Pre, "."), strings.Split(w.Pre, ".")
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePart(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// comparePart compares two pre-release parts: numbers by value and
// before words, words by their bytes.
func comparePart(a, b string) int {
	na, ea := strconv.ParseUint(a, 10, 64)
	nb, eb := strconv.ParseUint(b, 10, 64)
	switch {
	case ea == nil && eb == nil:
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		}
		return 0
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// Range is a set of versions, like ^1.2 or >=1.0 <2.0.
type Range struct {
	text string
	all  []comparator // every one must hold
}

type comparator struct {
	op string // ">=", ">", "<=", "<", "="
	v  Version
}

func (c comparator) holds(v Version) bool {
	n := v.Compare(c.v)
	switch c.op {
	case ">=":
		return n >= 0
	case ">":
		return n > 0
	case "<=":
		return n <= 0
	case "<":
		return n < 0
	}
	return n == 0
}

// MaxRangeBytes bounds a range's text.
const MaxRangeBytes = 128

// ParseRange reads a version range. It is one or more of these, separated
// by spaces, all of which must hold:
//
//	1.2.3    exactly 1.2.3
//	^1.2     1.2.0 or newer, before 2.0.0 (^0.2 is before 0.3.0, ^0.0.3 only 0.0.3)
//	~1.2     1.2.0 or newer, before 1.3.0 (~1 is before 2.0.0)
//	1.x, 1   any 1.x.x (1.2.x: any 1.2.x)
//	*        any version
//	>=1.0 >1.0 <=2.1 <2  compared with the version, missing parts are 0
func ParseRange(s string) (Range, error) {
	if len(s) > MaxRangeBytes {
		return Range{}, fmt.Errorf("version range is longer than %d bytes", MaxRangeBytes)
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Range{}, errors.New("version range is empty")
	}
	r := Range{text: strings.Join(fields, " ")}
	for _, f := range fields {
		cs, err := parseTerm(f)
		if err != nil {
			return Range{}, fmt.Errorf("version range %q: %w", s, err)
		}
		r.all = append(r.all, cs...)
	}
	return r, nil
}

// MustRange is ParseRange for ranges known to be valid.
func MustRange(s string) Range {
	r, err := ParseRange(s)
	if err != nil {
		panic(err)
	}
	return r
}

// Contains reports whether v is in r.
func (r Range) Contains(v Version) bool {
	if len(r.all) == 0 {
		return false // the zero Range holds nothing
	}
	for _, c := range r.all {
		if !c.holds(v) {
			return false
		}
	}
	return true
}

func (r Range) String() string { return r.text }

// Compatible is the range of versions that can run what v ran: v and
// every newer version with the same major version (^v). For 0.y.z, where
// every minor version may break things, only the same 0.y.
func Compatible(v Version) Range {
	return MustRange("^" + v.String())
}

// parseTerm reads one space-separated part of a range.
func parseTerm(f string) ([]comparator, error) {
	op := ""
	for _, o := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(f, o) {
			op, f = o, f[len(o):]
			break
		}
	}
	if f == "*" || f == "x" || f == "X" {
		if op != "" && op != "=" {
			return nil, fmt.Errorf("%q: %s* is not a range", op+f, op)
		}
		return []comparator{{">=", Version{}}}, nil
	}
	v, n, pre, err := partial(f)
	if err != nil {
		return nil, err
	}
	if pre != "" && n < 3 {
		return nil, fmt.Errorf("%q: a pre-release needs all three numbers", f)
	}
	v.Pre = pre
	// next returns the first version after the first k numbers of v.
	next := func(k int) Version {
		switch k {
		case 1:
			return Version{Major: v.Major + 1, Pre: "0"}
		case 2:
			return Version{Major: v.Major, Minor: v.Minor + 1, Pre: "0"}
		}
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1, Pre: "0"}
	}
	switch op {
	case "", "=":
		if n == 3 {
			return []comparator{{"=", v}}, nil
		}
		return []comparator{{">=", v}, {"<", next(n)}}, nil
	case "^":
		k := 1 // the first number that is not 0 stays the same
		switch {
		case v.Major == 0 && (v.Minor != 0 || n < 3):
			k = 2
		case v.Major == 0:
			k = 3
		}
		if v.Major == 0 && n == 1 {
			k = 1
		}
		return []comparator{{">=", v}, {"<", next(k)}}, nil
	case "~":
		k := 2
		if n == 1 {
			k = 1
		}
		return []comparator{{">=", v}, {"<", next(k)}}, nil
	case "<=":
		if n < 3 {
			return []comparator{{"<", next(n)}}, nil
		}
	case ">":
		if n < 3 {
			return []comparator{{">=", next(n)}}, nil
		}
	}
	return []comparator{{op, v}}, nil
}

// partial reads 1, 1.2, 1.2.3, 1.x, 1.2.x or 1.2.3-pre: the version with
// missing parts 0, how many numbers were given, and the pre-release.
func partial(s string) (Version, int, string, error) {
	bad := fmt.Errorf("%q is not a version like 1, 1.2, 1.2.3 or 1.x", s)
	main, pre, hasPre := strings.Cut(s, "-")
	if hasPre {
		if _, err := ParseVersion("0.0.0-" + pre); err != nil || pre == "" {
			return Version{}, 0, "", bad
		}
	}
	parts := strings.Split(main, ".")
	if len(parts) > 3 {
		return Version{}, 0, "", bad
	}
	var nums []uint64
	for i, p := range parts {
		if p == "x" || p == "X" || p == "*" {
			if i == 0 {
				return Version{}, 0, "", bad
			}
			for _, rest := range parts[i:] {
				if rest != "x" && rest != "X" && rest != "*" {
					return Version{}, 0, "", bad
				}
			}
			if hasPre {
				return Version{}, 0, "", bad
			}
			break
		}
		if p == "" || (len(p) > 1 && p[0] == '0') || strings.TrimLeft(p, "0123456789") != "" {
			return Version{}, 0, "", bad
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || n > MaxVersionNumber {
			return Version{}, 0, "", bad
		}
		nums = append(nums, n)
	}
	var v Version
	for i, n := range nums {
		*[]*uint64{&v.Major, &v.Minor, &v.Patch}[i] = n
	}
	return v, len(nums), pre, nil
}
