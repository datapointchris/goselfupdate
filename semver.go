package goselfupdate

import (
	"strconv"
	"strings"
)

// Semantic version comparison, implemented here rather than taken from
// golang.org/x/mod/semver so that this package has no third-party
// dependencies. x/mod raises its own minimum Go version over time, which for a
// library means periodically excluding callers on a supported Go release.
//
// Precedence follows https://semver.org section 11.

type version struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease string
}

// parseVersion accepts an optional leading "v" and one to three numeric
// components, so "v1", "1.2" and "v1.2.3-rc.1+build" all parse. Absent
// components are zero.
func parseVersion(text string) (version, bool) {
	text = strings.TrimPrefix(text, "v")
	if text == "" {
		return version{}, false
	}

	// Build metadata is ignored entirely for precedence.
	if plus := strings.IndexByte(text, '+'); plus >= 0 {
		text = text[:plus]
	}

	var prerelease string
	if dash := strings.IndexByte(text, '-'); dash >= 0 {
		prerelease = text[dash+1:]
		text = text[:dash]
		if !isValidPrerelease(prerelease) {
			return version{}, false
		}
	}

	fields := strings.Split(text, ".")
	if len(fields) > 3 {
		return version{}, false
	}

	var parsed version
	targets := []*uint64{&parsed.major, &parsed.minor, &parsed.patch}
	for index, field := range fields {
		number, ok := parseNumeric(field)
		if !ok {
			return version{}, false
		}
		*targets[index] = number
	}

	parsed.prerelease = prerelease
	return parsed, true
}

// parseNumeric rejects the leading zeros that the specification disallows, so
// "01" is not a valid major version.
func parseNumeric(field string) (uint64, bool) {
	if field == "" || (len(field) > 1 && field[0] == '0') {
		return 0, false
	}
	number, err := strconv.ParseUint(field, 10, 64)
	if err != nil {
		return 0, false
	}
	return number, true
}

func isValidPrerelease(prerelease string) bool {
	if prerelease == "" {
		return false
	}
	for _, identifier := range strings.Split(prerelease, ".") {
		if identifier == "" {
			return false
		}
		for index := 0; index < len(identifier); index++ {
			if !isIdentifierByte(identifier[index]) {
				return false
			}
		}
		// A numeric identifier may not carry leading zeros.
		if isNumericIdentifier(identifier) && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func isIdentifierByte(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
		return true
	default:
		return false
	}
}

func isNumericIdentifier(identifier string) bool {
	for index := 0; index < len(identifier); index++ {
		if identifier[index] < '0' || identifier[index] > '9' {
			return false
		}
	}
	return true
}

func isValidVersion(text string) bool {
	_, ok := parseVersion(text)
	return ok
}

// compareVersion returns -1, 0 or +1 as a is less than, equal to, or greater
// than b. Invalid versions sort below valid ones.
func compareVersion(a, b string) int {
	left, leftOK := parseVersion(a)
	right, rightOK := parseVersion(b)
	switch {
	case !leftOK && !rightOK:
		return 0
	case !leftOK:
		return -1
	case !rightOK:
		return 1
	}

	if result := compareUint(left.major, right.major); result != 0 {
		return result
	}
	if result := compareUint(left.minor, right.minor); result != 0 {
		return result
	}
	if result := compareUint(left.patch, right.patch); result != 0 {
		return result
	}
	return comparePrerelease(left.prerelease, right.prerelease)
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// comparePrerelease implements the precedence rules for pre-release
// identifiers: a version with a pre-release ranks below the same version
// without one, numeric identifiers compare numerically and rank below
// alphanumeric ones, and where all preceding identifiers are equal the longer
// list wins.
func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}

	left := strings.Split(a, ".")
	right := strings.Split(b, ".")

	for index := 0; index < len(left) && index < len(right); index++ {
		if result := compareIdentifier(left[index], right[index]); result != 0 {
			return result
		}
	}

	return compareUint(uint64(len(left)), uint64(len(right)))
}

func compareIdentifier(a, b string) int {
	aNumeric := isNumericIdentifier(a)
	bNumeric := isNumericIdentifier(b)

	switch {
	case aNumeric && bNumeric:
		// Parsed rather than compared as strings so that 2 ranks below 11.
		left, _ := strconv.ParseUint(a, 10, 64)
		right, _ := strconv.ParseUint(b, 10, 64)
		return compareUint(left, right)
	case aNumeric:
		return -1
	case bNumeric:
		return 1
	default:
		return strings.Compare(a, b)
	}
}
