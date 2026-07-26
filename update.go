package goselfupdate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Result describes what an update found, whether or not it installed anything.
type Result struct {
	// From is the running version, canonicalised with a leading "v".
	From string

	// To is the version now installed, or the version that would be installed
	// by [Update] after a [Check]. It equals From when nothing is newer.
	To string

	// Applied reports whether a new binary was written.
	Applied bool

	// Release is the release To was taken from. Its zero value means no
	// release was newer.
	Release Release
}

// UpdateAvailable reports whether a newer version was found.
func (r Result) UpdateAvailable() bool {
	return r.From != r.To
}

// Check reports whether a newer release exists, without downloading an asset
// or touching the filesystem.
func Check(ctx context.Context, cfg Config) (Result, error) {
	resolved, err := cfg.resolve()
	if err != nil {
		return Result{}, err
	}

	current, err := currentVersion(resolved.Version)
	if err != nil {
		return Result{}, err
	}

	release, err := resolved.Source.LatestRelease(ctx)
	if err != nil {
		return Result{}, err
	}

	latest := canonical(release.Tag)
	if !isValidVersion(latest) {
		return Result{}, fmt.Errorf("%w: tag %q is not a semantic version", ErrNoRelease, release.Tag)
	}
	if compareVersion(latest, current) <= 0 {
		return Result{From: current, To: current}, nil
	}

	return Result{From: current, To: latest, Release: release}, nil
}

// Update replaces the running executable with the latest release.
//
// It is a no-op returning a Result with Applied false when the running version
// is already current. Symbolic links are resolved, so an update rewrites the
// real file rather than replacing a link with a binary.
func Update(ctx context.Context, cfg Config) (Result, error) {
	target, err := runningBinary()
	if err != nil {
		return Result{}, err
	}
	return UpdateTo(ctx, cfg, target)
}

// UpdateTo is [Update] against an explicit path rather than the running
// executable. It is what to call to update a binary other than this one, and
// what tests use to avoid replacing the test binary.
func UpdateTo(ctx context.Context, cfg Config, target string) (Result, error) {
	resolved, err := cfg.resolve()
	if err != nil {
		return Result{}, err
	}

	result, err := Check(ctx, resolved)
	if err != nil || !result.UpdateAvailable() {
		return result, err
	}

	asset, err := result.Release.platformAsset()
	if err != nil {
		return Result{}, err
	}

	data, err := resolved.Source.Download(ctx, asset)
	if err != nil {
		return Result{}, fmt.Errorf("download %s: %w", asset.Name, err)
	}

	// Verification precedes extraction so that no unverified bytes are ever
	// parsed by an archive reader or written to disk.
	if err := resolved.Verifier.Verify(ctx, resolved.Source, result.Release, asset, data); err != nil {
		return Result{}, err
	}

	binary, err := extractBinary(asset, data, resolved.Binary)
	if err != nil {
		return Result{}, err
	}

	if err := replaceBinary(target, binary); err != nil {
		return Result{}, err
	}

	result.Applied = true
	return result, nil
}

// Changelog returns the commit subjects between two versions when the
// configured [Source] implements [Changeloger], and nil when it does not.
func Changelog(ctx context.Context, cfg Config, fromTag, toTag string) ([]string, error) {
	resolved, err := cfg.resolve()
	if err != nil {
		return nil, err
	}

	changeloger, ok := resolved.Source.(Changeloger)
	if !ok {
		return nil, nil
	}
	return changeloger.Changelog(ctx, fromTag, toTag)
}

// currentVersion canonicalises the running build's version and rejects one
// that carries none.
func currentVersion(version string) (string, error) {
	canonicalised := canonical(version)
	if !isValidVersion(canonicalised) {
		return "", fmt.Errorf("%w: version %q", ErrDevBuild, version)
	}
	return canonicalised, nil
}

// canonical adds the leading "v" this package prints versions with.
// goreleaser configurations inject {{.Tag}} in some projects and {{.Version}}
// in others, so both forms reach this package.
func canonical(version string) string {
	if version == "" || version[0] == 'v' {
		return version
	}
	return "v" + version
}

func runningBinary() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	return resolved, nil
}
