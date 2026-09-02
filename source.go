package goselfupdate

import (
	"context"
	"fmt"
	"runtime"
	"strings"
)

// Source locates and fetches releases. [GitHubSource] is the provided
// implementation; supply another through [Config] to update from a different
// forge or from a private mirror.
type Source interface {
	// LatestRelease returns the newest release the source knows about. It
	// returns [ErrNoRelease] when there is none.
	LatestRelease(ctx context.Context) (Release, error)

	// Download fetches an asset's bytes.
	Download(ctx context.Context, asset Asset) ([]byte, error)
}

// Changeloger is an optional interface a [Source] may implement to describe
// what changed between two versions. A source that does not implement it
// simply produces no changelog, and [Changelog] returns none.
type Changeloger interface {
	Changelog(ctx context.Context, fromTag, toTag string) ([]string, error)
}

// Release is one published version and the files attached to it.
type Release struct {
	// Tag is the release's version, with or without a leading "v".
	Tag string

	// Assets are the files published with the release.
	Assets []Asset

	// Prerelease reports whether the source marked this release as a
	// prerelease.
	Prerelease bool
}

// Asset is one file attached to a [Release].
type Asset struct {
	// Name is the file name, used to match the running platform.
	Name string

	// URL is where [Source.Download] fetches the asset from.
	URL string

	// Size is the asset's length in bytes, or zero if the source does not
	// report it.
	Size int64
}

// platformAsset picks the asset for the running GOOS/GOARCH.
//
// Matching is on the asset names the source reports rather than on a name
// rebuilt from a template, so a project that changes its archive naming does
// not silently break. An ambiguous match is an error rather than a guess: the
// cost of picking wrong is overwriting a working binary with one built for
// another architecture.
func (r Release) platformAsset() (Asset, error) {
	// An exact architecture match wins outright. Universal builds are only
	// considered when there is no native asset, so a release carrying both
	// darwin_arm64 and darwin_all resolves rather than reporting ambiguity.
	matches := r.matchPlatform(archAliases())
	if len(matches) == 0 {
		matches = r.matchPlatform(universalArchAliases())
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Asset{}, fmt.Errorf("%w: release %s carries nothing for %s/%s",
			ErrNoAsset, r.Tag, runtime.GOOS, runtime.GOARCH)
	default:
		return Asset{}, fmt.Errorf("%w: release %s has %d assets for %s/%s: %s",
			ErrAmbiguousAsset, r.Tag, len(matches), runtime.GOOS, runtime.GOARCH,
			strings.Join(assetNames(matches), ", "))
	}
}

func (r Release) matchPlatform(arches []string) []Asset {
	var matches []Asset
	for _, asset := range r.Assets {
		if isChecksumFile(asset.Name) {
			continue
		}
		if matchesPlatform(asset.Name, arches) {
			matches = append(matches, asset)
		}
	}
	return matches
}

// matchesPlatform reports whether an asset name names the running OS and one
// of the given architectures. Separators vary between projects
// (todoui_1.0.0_darwin_arm64.tar.gz, tool-1.0.0-darwin-arm64.zip), so both are
// matched as delimited words rather than as a fixed suffix.
func matchesPlatform(name string, arches []string) bool {
	lower := strings.ToLower(name)

	matchedOS := false
	for _, alias := range osAliases() {
		if containsWord(lower, alias) {
			matchedOS = true
			break
		}
	}
	if !matchedOS {
		return false
	}

	for _, arch := range arches {
		if containsWord(lower, arch) {
			return true
		}
	}
	return false
}

func osAliases() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"darwin", "macos", "mac", "osx"}
	case "windows":
		return []string{"windows", "win"}
	default:
		return []string{runtime.GOOS}
	}
}

func archAliases() []string {
	switch runtime.GOARCH {
	case "amd64":
		return []string{"amd64", "x86_64", "x8664", "64bit"}
	case "386":
		// Deliberately no bare "x86": it appears inside "x86_64" delimited by
		// an underscore, so a 386 binary would match a 64-bit asset.
		return []string{"386", "i386", "32bit"}
	case "arm64":
		return []string{"arm64", "aarch64"}
	default:
		return []string{runtime.GOARCH}
	}
}

// universalArchAliases name architecture-independent builds. goreleaser emits
// darwin_all for a macOS universal binary.
func universalArchAliases() []string {
	if runtime.GOOS == "darwin" {
		return []string{"all", "universal"}
	}
	return nil
}

// containsWord reports whether word appears in name delimited by separators
// rather than as a substring, so "arm64" does not match inside "armv6" and
// "mac" does not match inside "macro".
func containsWord(name, word string) bool {
	for index := 0; ; {
		found := strings.Index(name[index:], word)
		if found < 0 {
			return false
		}
		start := index + found
		end := start + len(word)
		if isBoundary(name, start-1) && isBoundary(name, end) {
			return true
		}
		index = start + 1
	}
}

func isBoundary(name string, index int) bool {
	if index < 0 || index >= len(name) {
		return true
	}
	switch c := name[index]; {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return false
	default:
		return true
	}
}

func isChecksumFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "checksum") ||
		strings.HasSuffix(lower, ".sha256") ||
		strings.HasSuffix(lower, ".sig") ||
		strings.HasSuffix(lower, ".pem") ||
		strings.HasSuffix(lower, ".sbom.json")
}

func assetNames(assets []Asset) []string {
	names := make([]string, 0, len(assets))
	for _, asset := range assets {
		names = append(names, asset.Name)
	}
	return names
}
