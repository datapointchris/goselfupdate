package goselfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// newStubRelease builds a release carrying a platform archive and a matching
// checksums file, which is the shape goreleaser publishes.
func newStubRelease(t *testing.T, tag string) *stubSource {
	t.Helper()

	archive := makeTarGz(t, map[string]string{"tool": binaryContent})
	archiveName := platformName("tool_%s_%s.tar.gz")
	checksumsName := "tool_checksums.txt"

	return &stubSource{
		release: Release{
			Tag:    tag,
			Assets: []Asset{{Name: archiveName}, {Name: checksumsName}},
		},
		bodies: map[string][]byte{
			archiveName:   archive,
			checksumsName: []byte(fmt.Sprintf("%s  %s\n", digestOf(archive), archiveName)),
		},
	}
}

func stubConfig(source Source, version string) Config {
	return Config{Binary: "tool", Version: version, Source: source}
}

func stageBinary(t *testing.T) string {
	t.Helper()

	target := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestUpdateToInstallsNewerRelease(t *testing.T) {
	source := newStubRelease(t, "v1.2.0")
	target := stageBinary(t)

	result, err := UpdateTo(context.Background(), stubConfig(source, "v1.1.0"), target)
	if err != nil {
		t.Fatalf("UpdateTo: %v", err)
	}
	if !result.Applied {
		t.Error("Applied should be true")
	}
	if result.From != "v1.1.0" || result.To != "v1.2.0" {
		t.Errorf("got %s → %s", result.From, result.To)
	}

	installed, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != binaryContent {
		t.Errorf("binary not replaced: %q", installed)
	}
}

func TestUpdateToIsANoopWhenCurrent(t *testing.T) {
	for _, running := range []string{"v1.2.0", "v1.3.0"} {
		source := newStubRelease(t, "v1.2.0")
		target := stageBinary(t)

		result, err := UpdateTo(context.Background(), stubConfig(source, running), target)
		if err != nil {
			t.Fatalf("running %s: %v", running, err)
		}
		if result.Applied || result.UpdateAvailable() {
			t.Errorf("running %s: unexpectedly updated", running)
		}

		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "old binary" {
			t.Errorf("running %s: binary was replaced", running)
		}
	}
}

func TestUpdateToRejectsDevBuilds(t *testing.T) {
	for _, running := range []string{"", "dev", "(devel)", "unknown"} {
		source := newStubRelease(t, "v1.2.0")
		_, err := UpdateTo(context.Background(), stubConfig(source, running), stageBinary(t))
		if !errors.Is(err, ErrDevBuild) {
			t.Errorf("version %q: got %v, want ErrDevBuild", running, err)
		}
	}
}

// A bad checksum must stop the update before the existing binary is touched.
func TestUpdateToLeavesBinaryIntactOnChecksumMismatch(t *testing.T) {
	source := newStubRelease(t, "v1.2.0")
	source.bodies["tool_checksums.txt"] = []byte("0000  " + platformName("tool_%s_%s.tar.gz") + "\n")

	target := stageBinary(t)

	_, err := UpdateTo(context.Background(), stubConfig(source, "v1.1.0"), target)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old binary" {
		t.Error("binary was replaced despite a failed checksum")
	}
}

func TestUpdateToPropagatesSourceFailure(t *testing.T) {
	sentinel := errors.New("network down")
	source := &stubSource{err: sentinel}

	_, err := UpdateTo(context.Background(), stubConfig(source, "v1.0.0"), stageBinary(t))
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want the source error", err)
	}
}

func TestUpdateToRejectsNonSemverTag(t *testing.T) {
	source := newStubRelease(t, "not-a-version")

	_, err := UpdateTo(context.Background(), stubConfig(source, "v1.0.0"), stageBinary(t))
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("got %v, want ErrNoRelease", err)
	}
}

func TestUpdateToHonoursCustomVerifier(t *testing.T) {
	source := newStubRelease(t, "v1.2.0")
	// Remove the checksums file so the default verifier would fail.
	source.release.Assets = source.release.Assets[:1]

	cfg := stubConfig(source, "v1.1.0")
	cfg.Verifier = NoVerification{}

	result, err := UpdateTo(context.Background(), cfg, stageBinary(t))
	if err != nil {
		t.Fatalf("UpdateTo: %v", err)
	}
	if !result.Applied {
		t.Error("expected the update to be applied")
	}
}

func TestCheckDoesNotWrite(t *testing.T) {
	source := newStubRelease(t, "v2.0.0")

	result, err := Check(context.Background(), stubConfig(source, "v1.0.0"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Applied {
		t.Error("Check reported an applied update")
	}
	if !result.UpdateAvailable() || result.To != "v2.0.0" {
		t.Errorf("got %+v", result)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := map[string]Config{
		"no binary":        {Owner: "o", Repo: "r", Version: "v1.0.0"},
		"no owner or repo": {Binary: "tool", Version: "v1.0.0"},
	}
	for name, cfg := range cases {
		if _, err := Check(context.Background(), cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: got %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestConfigDefaultsToGitHubSource(t *testing.T) {
	resolved, err := Config{Owner: "o", Repo: "r", Binary: "tool", Version: "v1.0.0"}.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resolved.Source.(*GitHubSource); !ok {
		t.Errorf("Source is %T, want *GitHubSource", resolved.Source)
	}
	if _, ok := resolved.Verifier.(ChecksumVerifier); !ok {
		t.Errorf("Verifier is %T, want ChecksumVerifier", resolved.Verifier)
	}
	if resolved.HTTPClient == nil {
		t.Error("HTTPClient was not defaulted")
	}
}

func TestChangelogReturnsNilForSourceWithout(t *testing.T) {
	// A Source that does not implement Changeloger produces no changelog
	// rather than an error.
	cfg := Config{Binary: "tool", Version: "v1.0.0", Source: sourceWithoutChangelog{}}

	subjects, err := Changelog(context.Background(), cfg, "v1.0.0", "v1.1.0")
	if err != nil {
		t.Fatalf("Changelog: %v", err)
	}
	if subjects != nil {
		t.Errorf("got %v, want nil", subjects)
	}
}

type sourceWithoutChangelog struct{}

func (sourceWithoutChangelog) LatestRelease(context.Context) (Release, error) {
	return Release{Tag: "v1.0.0"}, nil
}

func (sourceWithoutChangelog) Download(context.Context, Asset) ([]byte, error) {
	return nil, nil
}
