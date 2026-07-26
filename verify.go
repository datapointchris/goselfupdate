package goselfupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Verifier establishes that a downloaded asset is the one the release
// published. It runs before anything is extracted, so a failure means no
// untrusted bytes are ever parsed or written.
type Verifier interface {
	// Verify returns nil if data is authentic. release and asset describe
	// where the data came from, so an implementation can fetch a checksum
	// file, signature or manifest from the same release.
	Verify(ctx context.Context, source Source, release Release, asset Asset, data []byte) error
}

// ChecksumVerifier checks an asset's SHA-256 against the checksum file
// published with the release. This is the default.
//
// It defends against a corrupted, truncated or intercepted download, given
// that the checksum file itself is fetched over TLS from the same release. It
// does not defend against a compromised publishing account, which can rewrite
// the checksum file alongside the asset — that requires a signature and a key
// distributed out of band.
type ChecksumVerifier struct{}

// Verify implements [Verifier].
func (ChecksumVerifier) Verify(ctx context.Context, source Source, release Release, asset Asset, data []byte) error {
	checksums, ok := checksumAsset(release)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoChecksums, release.Tag)
	}

	body, err := source.Download(ctx, checksums)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", checksums.Name, err)
	}

	want, err := findChecksum(body, asset.Name)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, want) {
		return fmt.Errorf("%w for %s: published %s, downloaded %s",
			ErrChecksumMismatch, asset.Name, want, got)
	}
	return nil
}

// NoVerification disables integrity checking. Supplying it means trusting the
// transport alone; prefer a [Verifier] that checks something.
type NoVerification struct{}

// Verify implements [Verifier] by accepting anything.
func (NoVerification) Verify(context.Context, Source, Release, Asset, []byte) error {
	return nil
}

func checksumAsset(release Release) (Asset, bool) {
	for _, asset := range release.Assets {
		if strings.Contains(strings.ToLower(asset.Name), "checksum") {
			return asset, true
		}
	}
	return Asset{}, false
}

// findChecksum reads the sha256sum format that goreleaser and coreutils both
// emit: a hex digest, whitespace, an optional binary marker, then the file name.
//
// The name may carry a path. sha256sum echoes its arguments verbatim, so a CI
// step written as `sha256sum ./*.tar.gz` — a natural way to write it, and what
// several of these releases already publish — records "./tool_1.0.0.tar.gz"
// while the release asset is named "tool_1.0.0.tar.gz". Both name the same
// file, so the directory part is not part of the identity.
//
// An exact match still wins outright. The base name is only consulted when
// nothing matched exactly, so a checksums file that deliberately distinguishes
// two paths is never resolved by guessing between them.
func findChecksum(checksums []byte, assetName string) (string, error) {
	var byBase string

	scanner := bufio.NewScanner(bytes.NewReader(checksums))
	for scanner.Scan() {
		digest, name, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if !ok {
			continue
		}
		name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "*"))
		if name == assetName {
			return digest, nil
		}
		if byBase == "" && baseName(name) == assetName {
			byBase = digest
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	if byBase != "" {
		return byBase, nil
	}
	return "", fmt.Errorf("%w: no entry for %s", ErrNoChecksums, assetName)
}

// baseName strips a leading directory from a checksums entry. Both separators
// are honored because the file is read on whichever platform is updating, not
// the one that produced it — a Linux runner's "./x" has to resolve on Windows.
func baseName(name string) string {
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		return name[index+1:]
	}
	return name
}
