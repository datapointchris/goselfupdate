# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.2.1] - 2026-07-26

### Fixed

- A checksums entry naming a path (`./tool_1.0.0_linux_amd64.tar.gz`, as
  `sha256sum ./*.tar.gz` records it) no longer fails verification with
  `ErrNoChecksums`. `sha256sum` echoes its arguments verbatim, so the directory
  part is an artifact of how the CI step was written, not part of the file's
  identity. An exact match still wins outright; the base name is consulted only
  when nothing matched exactly, so a file deliberately distinguishing two paths
  is never resolved by guessing. Both separators are honored, since a Linux
  runner's `./x` has to resolve on Windows.

## [0.2.0] - 2026-07-26

### Added

- `Config.TagPrefix` and `GitHubSource.TagPrefix`, selecting one release stream
  in a repository that publishes several (`cli/v1.2.3` alongside `v9.0.0`). Go
  requires a module in a subdirectory to be tagged with it and goreleaser calls
  it a monorepo tag prefix, but `releases/latest` is repository-wide, so
  without this a CLI released from an application's repository resolved that
  application's release and failed as "not a semantic version". Reported tags
  have the prefix removed, leaving `Release.Tag` a version as documented, and
  `Changelog` restores it when resolving git refs.

  Selection within a prefixed stream is by highest version rather than by
  position in the list, so a patch to an older line published after a newer
  minor is not offered as an update. The unprefixed path is untouched.

### Fixed

- The package documentation claimed versions were compared with
  `golang.org/x/mod/semver`. They are compared by the implementation in
  `semver.go`, which exists precisely so the package has no third-party
  dependencies.

## [0.1.0] - 2026-07-26

Initial release.

### Added

- `Update`, `UpdateTo` and `Check` for replacing a running binary with the
  latest GitHub release.
- `cobracmd` subpackage providing an `update` command with an `upgrade` alias
  and a `--check` flag, kept separate so the core imports no CLI framework.
- Asset discovery from the release API, matching OS and architecture as
  delimited words. Handles `_` and `-` separators, `x86_64`/`amd64`,
  `aarch64`/`arm64` and `macos`/`darwin`, and falls back to a macOS universal
  build only when no native asset exists.
- `.tar.gz`, `.tgz`, `.zip` and bare-binary release assets, with the binary at
  the archive root or nested, and a tolerated `.exe` suffix.
- SHA-256 verification against the release's checksum file, run before
  extraction. `Verifier` is the interface for signature verification.
- Windows support: the running executable is displaced rather than overwritten,
  and `CleanupOldBinary` removes the displaced copy on the next start.
- Sentinel errors for every failure mode, so callers branch with `errors.Is`
  rather than on message text.
- `Token` support, defaulting to `$GITHUB_TOKEN` then `$GH_TOKEN`, for GitHub's
  rate limit and private repositories. `GitHubSource.APIBase` targets GitHub
  Enterprise.
- `AllowPrerelease` to select the newest release including prereleases.

### Notes

- The core package has no third-party dependencies. Semantic version
  comparison is implemented in-package rather than taken from
  `golang.org/x/mod/semver`, which raises its minimum Go version over time and
  would force that floor onto every caller.
- `golang.org/x/crypto/openpgp` is deliberately not linked. It is unmaintained
  and carries [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932), which has
  no fixed version, so depending on it makes `govulncheck` permanently fail.

[Unreleased]: https://github.com/datapointchris/goselfupdate/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/datapointchris/goselfupdate/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/datapointchris/goselfupdate/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/datapointchris/goselfupdate/releases/tag/v0.1.0
