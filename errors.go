package goselfupdate

import "errors"

var (
	// ErrDevBuild is returned when the running version is empty, "dev", or
	// anything else that does not parse as semver. With nothing to compare, an
	// update would discard a local build for a release that may be older. A Go
	// pseudo-version parses as semver and is not refused here;
	// [IsReleaseVersion] is the test that rejects it.
	ErrDevBuild = errors.New("cannot update a development build")

	// ErrNoRelease is returned when the source publishes no usable release.
	ErrNoRelease = errors.New("no release found")

	// ErrNoAsset is returned when a release carries nothing for the running
	// platform.
	ErrNoAsset = errors.New("no release asset for this platform")

	// ErrAmbiguousAsset is returned when more than one asset matches the
	// running platform. Choosing between them by guessing risks installing a
	// binary for the wrong architecture over a working one.
	ErrAmbiguousAsset = errors.New("multiple release assets match this platform")

	// ErrNoChecksums is returned when a release publishes no checksum file and
	// no alternative [Verifier] was configured.
	ErrNoChecksums = errors.New("release publishes no checksum file")

	// ErrChecksumMismatch is returned when a downloaded asset does not match
	// its published checksum. Nothing is extracted or installed.
	ErrChecksumMismatch = errors.New("checksum mismatch")

	// ErrBinaryNotFound is returned when the downloaded archive does not
	// contain the configured binary.
	ErrBinaryNotFound = errors.New("binary not found in release archive")

	// ErrInvalidConfig is returned when a [Config] is missing a required field.
	ErrInvalidConfig = errors.New("invalid config")
)
