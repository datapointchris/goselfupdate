package goselfupdate

import "testing"

// The ordering published in https://semver.org section 11, which is the
// authority this implementation is written against.
var specPrecedence = []string{
	"1.0.0-alpha",
	"1.0.0-alpha.1",
	"1.0.0-alpha.beta",
	"1.0.0-beta",
	"1.0.0-beta.2",
	"1.0.0-beta.11",
	"1.0.0-rc.1",
	"1.0.0",
	"1.0.1",
	"1.1.0",
	"2.0.0",
}

func TestCompareVersionFollowsSpecPrecedence(t *testing.T) {
	for i := range specPrecedence {
		for j := range specPrecedence {
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}

			got := compareVersion(specPrecedence[i], specPrecedence[j])
			if got != want {
				t.Errorf("compareVersion(%q, %q) = %d, want %d",
					specPrecedence[i], specPrecedence[j], got, want)
			}
		}
	}
}

func TestCompareVersionIgnoresBuildMetadata(t *testing.T) {
	pairs := [][2]string{
		{"1.0.0+build.1", "1.0.0+build.2"},
		{"v1.2.3+abc", "1.2.3"},
		{"1.0.0-rc.1+x", "1.0.0-rc.1+y"},
	}
	for _, pair := range pairs {
		if got := compareVersion(pair[0], pair[1]); got != 0 {
			t.Errorf("compareVersion(%q, %q) = %d, want 0", pair[0], pair[1], got)
		}
	}
}

// goreleaser injects {{.Tag}} in some projects and {{.Version}} in others, so
// the same version arrives with and without a leading v.
func TestCompareVersionIgnoresVPrefix(t *testing.T) {
	cases := [][3]any{
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "v1.2.3", 0},
		{"v1.2.4", "1.2.3", 1},
		{"1.2.3", "v1.2.4", -1},
	}
	for _, c := range cases {
		a := c[0].(string)
		b := c[1].(string)
		want := c[2].(int)
		if got := compareVersion(a, b); got != want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", a, b, got, want)
		}
	}
}

func TestCompareVersionNumericIdentifiersAreNotLexical(t *testing.T) {
	// The bug this guards: "11" sorts before "2" as a string.
	if got := compareVersion("1.0.0-beta.11", "1.0.0-beta.2"); got != 1 {
		t.Errorf("beta.11 vs beta.2 = %d, want 1", got)
	}
	if got := compareVersion("1.0.0-rc.9", "1.0.0-rc.10"); got != -1 {
		t.Errorf("rc.9 vs rc.10 = %d, want -1", got)
	}
}

func TestParseVersion(t *testing.T) {
	valid := map[string]version{
		"v1.2.3":         {major: 1, minor: 2, patch: 3},
		"1.2.3":          {major: 1, minor: 2, patch: 3},
		"v1":             {major: 1},
		"v1.2":           {major: 1, minor: 2},
		"v0.0.0":         {},
		"v1.2.3-rc.1":    {major: 1, minor: 2, patch: 3, prerelease: "rc.1"},
		"v1.2.3+build":   {major: 1, minor: 2, patch: 3},
		"v1.2.3-a.1+b.2": {major: 1, minor: 2, patch: 3, prerelease: "a.1"},
		"v10.20.30":      {major: 10, minor: 20, patch: 30},
	}
	for text, want := range valid {
		got, ok := parseVersion(text)
		if !ok {
			t.Errorf("parseVersion(%q) reported invalid", text)
			continue
		}
		if got != want {
			t.Errorf("parseVersion(%q) = %+v, want %+v", text, got, want)
		}
	}

	invalid := []string{
		"", "v", "dev", "(devel)", "latest",
		"1.2.3.4",    // too many components
		"01.2.3",     // leading zero
		"1.2.3-",     // empty prerelease
		"1.2.3-a..b", // empty identifier
		"1.2.3-01",   // numeric identifier with a leading zero
		"1.2.3-a_b",  // underscore is not an identifier character
		"x.y.z",
		"-1.2.3",
	}
	for _, text := range invalid {
		if _, ok := parseVersion(text); ok {
			t.Errorf("parseVersion(%q) reported valid", text)
		}
	}
}

func TestIsValidVersion(t *testing.T) {
	if !isValidVersion("v1.0.0") {
		t.Error("v1.0.0 should be valid")
	}
	for _, text := range []string{"", "dev", "(devel)"} {
		if isValidVersion(text) {
			t.Errorf("%q should be invalid", text)
		}
	}
}

func TestCompareVersionOrdersInvalidBelowValid(t *testing.T) {
	if got := compareVersion("dev", "v1.0.0"); got != -1 {
		t.Errorf("dev vs v1.0.0 = %d, want -1", got)
	}
	if got := compareVersion("v1.0.0", "dev"); got != 1 {
		t.Errorf("v1.0.0 vs dev = %d, want 1", got)
	}
	if got := compareVersion("dev", "nonsense"); got != 0 {
		t.Errorf("two invalid versions = %d, want 0", got)
	}
}

func FuzzParseVersion(f *testing.F) {
	for _, seed := range specPrecedence {
		f.Add(seed)
	}
	f.Add("v1.2.3+build")
	f.Add("")

	// Parsing must never panic, and a parsed version must compare equal to
	// itself no matter what it was built from.
	f.Fuzz(func(t *testing.T, text string) {
		if _, ok := parseVersion(text); !ok {
			return
		}
		if got := compareVersion(text, text); got != 0 {
			t.Errorf("compareVersion(%q, %q) = %d, want 0", text, text, got)
		}
	})
}
