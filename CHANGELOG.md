# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/datapointchris/goselfupdate/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/datapointchris/goselfupdate/releases/tag/v0.1.0
