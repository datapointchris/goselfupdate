# goselfupdate

[![Go Reference](https://pkg.go.dev/badge/github.com/datapointchris/goselfupdate.svg)](https://pkg.go.dev/github.com/datapointchris/goselfupdate)
[![CI](https://github.com/datapointchris/goselfupdate/actions/workflows/ci.yml/badge.svg)](https://github.com/datapointchris/goselfupdate/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/datapointchris/goselfupdate)](https://goreportcard.com/report/github.com/datapointchris/goselfupdate)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Self-updating Go binaries, for CLIs released with [goreleaser] and GitHub.

**No dependencies.** The core package imports only the standard library.

```go
result, err := goselfupdate.Update(ctx, goselfupdate.Config{
    Owner:   "datapointchris",
    Repo:    "todoui",
    Binary:  "todoui",
    Version: version, // your ldflags-injected build version
})
```

## Install

```sh
go get github.com/datapointchris/goselfupdate
```

## A ready-made cobra command

```go
import "github.com/datapointchris/goselfupdate/cobracmd"

root.AddCommand(cobracmd.New(goselfupdate.Config{
    Owner: "datapointchris", Repo: "todoui", Binary: "todoui", Version: version,
}))
```

```console
$ todoui update
✓ todoui upgraded: v1.6.0 → v1.6.1

Changes:
  • fix(sync): refresh from the API before CLI commands
  • docs: reference the nested icb projects items command

$ todoui update --check
✓ todoui update available: v1.6.0 → v1.6.1
```

`upgrade` is registered as an alias. This subpackage is the only thing that
imports cobra — projects using `flag`, `urfave/cli` or anything else pull in
nothing by importing the core.

## What it does

1. Reads the latest release from the GitHub API.
2. Compares it to the running version by semantic version precedence.
3. Picks the asset matching the running `GOOS`/`GOARCH`.
4. Verifies its SHA-256 against the release's checksums file.
5. Extracts the binary and replaces the running executable atomically.

## Design decisions

**Assets are discovered, not reconstructed.** Asset names come from the API and
are matched on delimited OS and architecture words, so projects naming archives
`tool_1.0.0_darwin_arm64.tar.gz` or `tool-1.0.0-macos-aarch64.zip` both work,
and a project that changes its naming does not silently break. An ambiguous
match is an error listing the candidates, never a guess — picking wrong means
overwriting a working binary with one built for another architecture.

**Verification precedes extraction.** Nothing unverified is passed to an
archive reader or written to disk.

**Replacement is atomic.** The new binary is staged in the target's own
directory, flushed to disk, and renamed into place, so an interrupted update
cannot leave a half-written executable. On Unix a running process holds its
executable by inode, so this is safe mid-execution. Windows locks the running
file, so the old binary is displaced first and cleaned up on the next start —
call [`CleanupOldBinary`] early in `main` to remove it.

**A build with no version refuses to update.** Comparing against a development
build is meaningless, and updating one would silently discard whatever local
build was in place.

## Supported release layouts

| Aspect | Supported |
|---|---|
| Archives | `.tar.gz`, `.tgz`, `.zip`, and bare uncompressed binaries |
| Platforms | Linux, macOS, Windows, and other Unix targets |
| Naming | `_` or `-` separated; `x86_64`/`amd64`, `aarch64`/`arm64`, `macos`/`darwin` |
| macOS | Universal (`darwin_all`) binaries, used only when no native asset exists |
| Nesting | Binary at the archive root or in a subdirectory; `.exe` tolerated |
| Checksums | `sha256sum` and goreleaser formats, with or without the binary-mode `*` |

goreleaser's defaults satisfy all of this with no configuration.

## Configuration

Everything beyond the four required fields has a working default.

| Field | Default | Purpose |
|---|---|---|
| `Token` | `$GITHUB_TOKEN`, then `$GH_TOKEN` | Raises GitHub's 60 requests/hour limit; required for private repositories |
| `HTTPClient` | 60-second timeout | Proxies, retries, custom timeouts |
| `Source` | GitHub | Another forge, or a private mirror |
| `Verifier` | `ChecksumVerifier` | Signature verification, or `NoVerification` |
| `AllowPrerelease` | `false` | Include prereleases when selecting the newest version |

GitHub Enterprise works by setting `GitHubSource.APIBase` to its `/api/v3` root.

## Errors

Failures are sentinels, so callers branch without matching message text:

```go
switch {
case errors.Is(err, goselfupdate.ErrDevBuild):
    // built from source
case errors.Is(err, goselfupdate.ErrNoAsset):
    // nothing published for this platform
case errors.Is(err, goselfupdate.ErrChecksumMismatch):
    // download did not match its published checksum
}
```

Full set: `ErrDevBuild`, `ErrNoRelease`, `ErrNoAsset`, `ErrAmbiguousAsset`,
`ErrNoChecksums`, `ErrChecksumMismatch`, `ErrBinaryNotFound`,
`ErrInvalidConfig`.

## Security model

Integrity comes from the release's checksum file, fetched over TLS from the
same release. This defends against a corrupted, truncated or intercepted
download. It does **not** defend against a compromised publishing account,
which can rewrite the checksum file alongside the asset — that requires a
signature verified against a key distributed out of band. Implement
[`Verifier`] to add one.

Deliberately absent: this package does not link `golang.org/x/crypto/openpgp`,
which is unmaintained and carries a permanent advisory ([GO-2026-5932]) with no
fixed version. A project depending on it cannot run `govulncheck` clean.

## Scope

Provided: GitHub releases and checksum verification. GitLab, Gitea and
signature verification are not implemented — [`Source`] and [`Verifier`] are
the interfaces to implement for them, and neither requires changes here.

Not supported: in-place binary patching, rollback to an arbitrary version, and
update channels beyond the prerelease toggle.

## Alternatives

| Package | Notes |
|---|---|
| [creativeprojects/go-selfupdate] | Broadest source support (GitHub, GitLab, Gitea, HTTP). Links `x/crypto/openpgp` for signature verification, so it carries [GO-2026-5932]; pulls in ~14 modules |
| [minio/selfupdate] | Applies an update with minisign verification. Does not locate releases — that half is yours to write |
| [sanbornm/go-selfupdate] | The original. Binary-diff patching against your own update server |

Use this one if you publish goreleaser archives to GitHub releases and want no
dependencies. Use `creativeprojects/go-selfupdate` if you need GitLab or Gitea.

## Requirements

- Go 1.23 or newer.
- Releases publishing per-platform archives and a checksums file.
- A version injected at build time, e.g.
  `-ldflags "-X main.version={{.Version}}"`.

## License

MIT

[goreleaser]: https://goreleaser.com
[GO-2026-5932]: https://pkg.go.dev/vuln/GO-2026-5932
[creativeprojects/go-selfupdate]: https://github.com/creativeprojects/go-selfupdate
[minio/selfupdate]: https://github.com/minio/selfupdate
[sanbornm/go-selfupdate]: https://github.com/sanbornm/go-selfupdate
[`Verifier`]: https://pkg.go.dev/github.com/datapointchris/goselfupdate#Verifier
[`Source`]: https://pkg.go.dev/github.com/datapointchris/goselfupdate#Source
[`CleanupOldBinary`]: https://pkg.go.dev/github.com/datapointchris/goselfupdate#CleanupOldBinary
