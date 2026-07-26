package goselfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// stubSource serves fixed assets without any network involved.
type stubSource struct {
	release   Release
	bodies    map[string][]byte
	changelog []string
	err       error
}

func (s *stubSource) LatestRelease(context.Context) (Release, error) {
	if s.err != nil {
		return Release{}, s.err
	}
	return s.release, nil
}

func (s *stubSource) Download(_ context.Context, asset Asset) ([]byte, error) {
	body, ok := s.bodies[asset.Name]
	if !ok {
		return nil, fmt.Errorf("no such asset: %s", asset.Name)
	}
	return body, nil
}

func (s *stubSource) Changelog(context.Context, string, string) ([]string, error) {
	return s.changelog, nil
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestChecksumVerifierAcceptsMatchingDigest(t *testing.T) {
	data := []byte(binaryContent)
	asset := Asset{Name: "tool_1.0.0_linux_amd64.tar.gz"}
	checksums := Asset{Name: "tool_1.0.0_checksums.txt"}

	source := &stubSource{
		bodies: map[string][]byte{
			checksums.Name: []byte(fmt.Sprintf("%s  %s\n", digestOf(data), asset.Name)),
		},
	}
	release := Release{Tag: "v1.0.0", Assets: []Asset{asset, checksums}}

	if err := (ChecksumVerifier{}).Verify(context.Background(), source, release, asset, data); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestChecksumVerifierRejectsMismatch(t *testing.T) {
	data := []byte(binaryContent)
	asset := Asset{Name: "tool_1.0.0_linux_amd64.tar.gz"}
	checksums := Asset{Name: "checksums.txt"}

	source := &stubSource{
		bodies: map[string][]byte{
			checksums.Name: []byte(fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), asset.Name)),
		},
	}
	release := Release{Tag: "v1.0.0", Assets: []Asset{asset, checksums}}

	err := (ChecksumVerifier{}).Verify(context.Background(), source, release, asset, data)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch", err)
	}
}

func TestChecksumVerifierRequiresAChecksumFile(t *testing.T) {
	asset := Asset{Name: "tool_1.0.0_linux_amd64.tar.gz"}
	release := Release{Tag: "v1.0.0", Assets: []Asset{asset}}

	err := (ChecksumVerifier{}).Verify(context.Background(), &stubSource{}, release, asset, []byte("x"))
	if !errors.Is(err, ErrNoChecksums) {
		t.Fatalf("got %v, want ErrNoChecksums", err)
	}
}

func TestChecksumVerifierRequiresAnEntryForTheAsset(t *testing.T) {
	asset := Asset{Name: "tool_1.0.0_linux_amd64.tar.gz"}
	checksums := Asset{Name: "checksums.txt"}
	source := &stubSource{
		bodies: map[string][]byte{
			checksums.Name: []byte("abc  some_other_file.tar.gz\n"),
		},
	}
	release := Release{Tag: "v1.0.0", Assets: []Asset{asset, checksums}}

	err := (ChecksumVerifier{}).Verify(context.Background(), source, release, asset, []byte("x"))
	if !errors.Is(err, ErrNoChecksums) {
		t.Fatalf("got %v, want ErrNoChecksums", err)
	}
}

// sha256sum, goreleaser and coreutils differ in spacing and in the binary-mode
// asterisk, and digests appear in either case.
func TestFindChecksumFormats(t *testing.T) {
	digest := digestOf([]byte(binaryContent))
	name := "tool_1.0.0_linux_amd64.tar.gz"

	formats := []string{
		digest + "  " + name,
		digest + " " + name,
		digest + " *" + name,
		strings.ToUpper(digest) + "  " + name,
		"# a comment\n" + digest + "  " + name,
		"other  file.tar.gz\n" + digest + "  " + name + "\nmore  x.tar.gz",
		// `sha256sum ./*.tar.gz` records the path it was handed. The directory
		// part is not part of the file's identity.
		digest + "  ./" + name,
		digest + "  dist/" + name,
		digest + "  *./" + name,
		digest + "  .\\" + name,
	}

	for _, checksums := range formats {
		got, err := findChecksum([]byte(checksums), name)
		if err != nil {
			t.Errorf("%q: %v", checksums, err)
			continue
		}
		if !strings.EqualFold(got, digest) {
			t.Errorf("%q: got %s", checksums, got)
		}
	}
}

func TestNoVerificationAcceptsAnything(t *testing.T) {
	err := NoVerification{}.Verify(context.Background(), nil, Release{}, Asset{}, []byte("anything"))
	if err != nil {
		t.Fatalf("NoVerification returned %v", err)
	}
}

// A path-carrying entry must never win over an exact one: if a checksums file
// distinguishes two paths that share a base name, resolving by base name would
// be a guess between them.
func TestFindChecksumPrefersExactOverBaseName(t *testing.T) {
	name := "tool_1.0.0_linux_amd64.tar.gz"
	exact := digestOf([]byte("exact"))
	nested := digestOf([]byte("nested"))

	checksums := nested + "  build/" + name + "\n" + exact + "  " + name + "\n"

	got, err := findChecksum([]byte(checksums), name)
	if err != nil {
		t.Fatalf("findChecksum: %v", err)
	}
	if got != exact {
		t.Errorf("got the nested entry %s, want the exact one %s", got, exact)
	}
}

func TestFindChecksumStillReportsAGenuineMiss(t *testing.T) {
	checksums := digestOf([]byte("x")) + "  some_other_tool.tar.gz\n"

	_, err := findChecksum([]byte(checksums), "tool_1.0.0_linux_amd64.tar.gz")
	if !errors.Is(err, ErrNoChecksums) {
		t.Fatalf("got %v, want ErrNoChecksums", err)
	}
}
