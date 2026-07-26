package goselfupdate

import (
	"errors"
	"fmt"
	"runtime"
	"testing"
)

// platformName builds an asset name for the running platform, so these tests
// assert real behavior on whatever machine they run on rather than pinning to
// one GOOS/GOARCH.
func platformName(format string) string {
	return fmt.Sprintf(format, runtime.GOOS, runtime.GOARCH)
}

func TestPlatformAssetPicksRunningPlatform(t *testing.T) {
	wanted := platformName("tool_1.0.0_%s_%s.tar.gz")
	release := Release{
		Tag: "v1.0.0",
		Assets: []Asset{
			{Name: "tool_1.0.0_plan9_mips.tar.gz"},
			{Name: wanted},
			{Name: "tool_1.0.0_checksums.txt"},
		},
	}

	asset, err := release.platformAsset()
	if err != nil {
		t.Fatalf("platformAsset: %v", err)
	}
	if asset.Name != wanted {
		t.Errorf("got %s, want %s", asset.Name, wanted)
	}
}

func TestPlatformAssetIgnoresChecksumFiles(t *testing.T) {
	// A checksums file naming the platform must never be mistaken for the
	// archive, or the updater would install the checksum list as a binary.
	release := Release{
		Tag: "v1.0.0",
		Assets: []Asset{
			{Name: platformName("tool_1.0.0_%s_%s.tar.gz")},
			{Name: platformName("tool_1.0.0_%s_%s.sha256")},
			{Name: platformName("tool_1.0.0_%s_%s.sig")},
		},
	}

	asset, err := release.platformAsset()
	if err != nil {
		t.Fatalf("platformAsset: %v", err)
	}
	if !hasExtension(asset.Name, ".tar.gz") {
		t.Errorf("picked %s, want the archive", asset.Name)
	}
}

func TestPlatformAssetReportsAmbiguity(t *testing.T) {
	release := Release{
		Tag: "v1.0.0",
		Assets: []Asset{
			{Name: platformName("tool_1.0.0_%s_%s.tar.gz")},
			{Name: platformName("tool_1.0.0_%s_%s.zip")},
		},
	}

	_, err := release.platformAsset()
	if !errors.Is(err, ErrAmbiguousAsset) {
		t.Fatalf("got %v, want ErrAmbiguousAsset", err)
	}
}

func TestPlatformAssetReportsMissing(t *testing.T) {
	release := Release{
		Tag:    "v1.0.0",
		Assets: []Asset{{Name: "tool_1.0.0_plan9_mips.tar.gz"}},
	}

	_, err := release.platformAsset()
	if !errors.Is(err, ErrNoAsset) {
		t.Fatalf("got %v, want ErrNoAsset", err)
	}
}

// goreleaser publishes macOS universal binaries as darwin_all. It should be
// used only when there is no native asset, not compete with one.
func TestPlatformAssetPrefersNativeOverUniversal(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("universal binaries are a darwin convention")
	}

	native := platformName("tool_1.0.0_%s_%s.tar.gz")
	release := Release{
		Tag: "v1.0.0",
		Assets: []Asset{
			{Name: "tool_1.0.0_darwin_all.tar.gz"},
			{Name: native},
		},
	}

	asset, err := release.platformAsset()
	if err != nil {
		t.Fatalf("platformAsset: %v", err)
	}
	if asset.Name != native {
		t.Errorf("got %s, want the native asset %s", asset.Name, native)
	}
}

func TestPlatformAssetFallsBackToUniversal(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("universal binaries are a darwin convention")
	}

	release := Release{
		Tag:    "v1.0.0",
		Assets: []Asset{{Name: "tool_1.0.0_darwin_all.tar.gz"}},
	}

	asset, err := release.platformAsset()
	if err != nil {
		t.Fatalf("platformAsset: %v", err)
	}
	if asset.Name != "tool_1.0.0_darwin_all.tar.gz" {
		t.Errorf("got %s", asset.Name)
	}
}

func TestMatchesPlatformSeparatorStyles(t *testing.T) {
	// Underscores, hyphens and no version at all are all in the wild.
	for _, format := range []string{
		"tool_1.0.0_%s_%s.tar.gz",
		"tool-1.0.0-%s-%s.zip",
		"tool.%s.%s",
		"TOOL_1.0.0_%s_%s.TAR.GZ",
	} {
		name := platformName(format)
		if !matchesPlatform(name, archAliases()) {
			t.Errorf("%q did not match %s/%s", name, runtime.GOOS, runtime.GOARCH)
		}
	}
}

func TestContainsWordRespectsBoundaries(t *testing.T) {
	cases := []struct {
		name string
		word string
		want bool
	}{
		{"tool_darwin_arm64.tar.gz", "arm64", true},
		{"tool_darwin_armv6.tar.gz", "arm64", false},
		{"tool_macos_arm64.zip", "macos", true},
		{"tool_macro_arm64.zip", "mac", false},
		{"tool-linux-amd64", "amd64", true},
		{"tool_linux_amd64", "linux", true},
		{"toollinux_amd64", "linux", false},
		{"tool_windows_386.zip", "386", true},
	}
	for _, c := range cases {
		if got := containsWord(c.name, c.word); got != c.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", c.name, c.word, got, c.want)
		}
	}
}

// A 386 build must not match an x86_64 asset. "x86" appears inside "x86_64"
// delimited by an underscore, which is why the bare alias is excluded.
func TestArchAliasesDoNotConfuse386WithX8664(t *testing.T) {
	for _, alias := range []string{"386", "i386", "32bit"} {
		if containsWord("tool_1.0.0_linux_x86_64.tar.gz", alias) {
			t.Errorf("alias %q matched an x86_64 asset", alias)
		}
	}
}

func TestIsChecksumFile(t *testing.T) {
	checksums := []string{
		"checksums.txt", "tool_1.0.0_checksums.txt", "CHECKSUMS.TXT",
		"tool.sha256", "tool.sig", "tool.pem", "tool.sbom.json",
	}
	for _, name := range checksums {
		if !isChecksumFile(name) {
			t.Errorf("%q should be treated as a checksum or signature file", name)
		}
	}

	archives := []string{"tool_1.0.0_linux_amd64.tar.gz", "tool.zip", "tool"}
	for _, name := range archives {
		if isChecksumFile(name) {
			t.Errorf("%q should not be treated as a checksum file", name)
		}
	}
}
