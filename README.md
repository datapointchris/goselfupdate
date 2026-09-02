# goselfupdate

[![Go Reference](https://pkg.go.dev/badge/github.com/datapointchris/goselfupdate.svg)](https://pkg.go.dev/github.com/datapointchris/goselfupdate)
[![CI](https://github.com/datapointchris/goselfupdate/actions/workflows/validate.yml/badge.svg)](https://github.com/datapointchris/goselfupdate/actions/workflows/validate.yml)
[![Bespoke CI](https://github.com/datapointchris/goselfupdate/actions/workflows/ci.yml/badge.svg)](https://github.com/datapointchris/goselfupdate/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/datapointchris/goselfupdate)](https://goreportcard.com/report/github.com/datapointchris/goselfupdate)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Self-updating Go binaries, for CLIs released with [goreleaser] and GitHub.

The core package and `autoupdate` import only the standard library. `cobracmd`
is the one that brings in [cobra].

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
✓ todoui updated: v1.6.0 → v1.6.1

Changes:
  • fix(sync): refresh from the API before CLI commands
  • docs: reference the nested icb projects items command

$ todoui update --check
✓ todoui update available: v1.6.0 → v1.6.1
```

The command carries no aliases. `update` is the fleet's one self-update verb,
and an alias is what let `upgrade` coexist with it across every CLI without
anyone choosing it. This subpackage is the only thing that imports cobra —
projects using `flag`, `urfave/cli` or anything else pull in nothing by
importing the core.

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
| --- | --- |
| Archives | `.tar.gz`, `.tgz`, `.zip`, and bare uncompressed binaries |
| Platforms | Linux, macOS, Windows, and other Unix targets |
| Naming | `_` or `-` separated; `x86_64`/`amd64`, `aarch64`/`arm64`, `macos`/`darwin` |
| macOS | Universal (`darwin_all`) binaries, used only when no native asset exists |
| Nesting | Binary at the archive root or in a subdirectory; `.exe` tolerated |
| Checksums | `sha256sum` and goreleaser formats, with or without the binary-mode `*` or a leading path |
| Tags | `v1.2.3`, or `cli/v1.2.3` in a repository publishing several components |

goreleaser's defaults satisfy all of this with no configuration.

### Repositories publishing more than one component

A repository that releases several things gives each its own tag prefix —
`cli/v1.2.3`, `api/v2.0.0`. Go requires it of a module in a subdirectory, and
goreleaser calls it a monorepo tag prefix. Set `TagPrefix` to pick a stream:

```go
goselfupdate.Config{
    Owner:     "datapointchris",
    Repo:      "meso",
    Binary:    "meso",
    Version:   version,
    TagPrefix: "cli/",
}
```

This is not only a parsing convenience. GitHub's "latest release" endpoint is
repository-wide, so without a prefix it returns whichever component released
most recently — a CLI would eventually try to install its own application's
release. A prefix switches to the release list, filters it, and picks the
highest version rather than the most recently created, so a patch to an older
line published after a newer minor is not offered as an update.

Tags come back with the prefix removed, so `Release.Tag`, `Result.From` and
`Result.To` are versions and the repository's tag layout stays internal.

## Configuration

Everything beyond the four required fields has a working default.

| Field | Default | Purpose |
| --- | --- | --- |
| `Token` | see Authentication below | A credential you already hold in code; you almost certainly want the default |
| `TokenFunc` | none | A source of your own, tried after the environment and before the command |
| `HTTPClient` | 60-second timeout | Proxies, retries, custom timeouts |
| `Source` | GitHub | Another forge, or a private mirror |
| `Verifier` | `ChecksumVerifier` | Signature verification, or `NoVerification` |
| `AllowPrerelease` | `false` | Include prereleases when selecting the newest version |
| `TagPrefix` | `""` | Select one release stream in a repository publishing several |

GitHub Enterprise works by setting `GitHubSource.APIBase` to its `/api/v3` root.

## Authentication

**Authenticated by default. Configure nothing.** `GitHubSource` runs `gh auth
token` when a request is about to be made, and sends what it prints.

The alternative is not "no credential". It is 60 requests an hour, charged **per
IP address** and shared with every other anonymous caller behind the same
egress. A default that has to be opted into is a default nobody sets.

Four sources, first non-empty wins:

| Source | Set by | Default |
| --- | --- | --- |
| `Config.Token` | you, in code | unset |
| `$GITHUB_TOKEN`, then `$GH_TOKEN` | whoever runs your CLI | unset |
| `TokenFunc()` | you, in code | unset |
| `$GITHUB_TOKEN_COMMAND` | whoever runs your CLI | `gh auth token` |

`$GITHUB_TOKEN_COMMAND` both redirects and disables, which is what a switch has
to do to be worth having:

```bash
GITHUB_TOKEN_COMMAND='pass show github/token'   # use this instead
GITHUB_TOKEN_COMMAND=''                         # run nothing, stay anonymous
```

Single and double quotes are honored; no shell runs the result, so pipelines
and expansion are not available. It never fails — a command that is not
installed, exits non-zero, or takes longer than ten seconds degrades to an
unauthenticated request, which still works against a public repository.

**This lives on `GitHubSource`, not on `Config`.** A credential is the host's
business — a `Source` for another forge brings its own variable and its own
command, and nothing above the `Source` interface learns either name.

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

## Telling the user without installing

`autoupdate` is the other half: it checks once a day and prints one line. It
never installs, so the `update` command above stays the only thing that writes
a binary.

```go
import "github.com/datapointchris/goselfupdate/autoupdate"

func main() {
    config := autoupdate.Config{Update: goselfupdate.Config{
        Owner: "you", Repo: "tool", Binary: "tool", Version: version,
    }}

    if err := cobracmd.Execute(context.Background(), rootCmd, config); err != nil {
        if errors.Is(err, cobracmd.ErrUsage) {
            os.Exit(2)
        }
        os.Exit(1)
    }
}
```

`ErrUsage` marks a failure caused by how the command was typed — an unknown or
malformed flag, or an unknown subcommand — rather than by the command running
and failing. Cobra returns both as ordinary errors, so without this every
failure flattens to exit 1 and a caller cannot tell "you typed it wrong" from
"it ran and failed". Only the former is worth retrying with different
arguments. Exit 2 is the shell convention, and what Python's argparse uses.

`Execute` also fills in what cobra leaves out of a typing mistake. An unknown
flag names the flags closest to what was typed, and both an unknown flag and an
unknown command name the command that lists the valid ones:

```text
$ tool search --ownd
error: unknown flag: --ownd

Did you mean this?
  --owned

Run 'tool search --help' for usage.
```

Suggestions respect the `DisableSuggestions` and `SuggestionsMinimumDistance`
already set on the command, so a mistyped flag and a mistyped command are
answered by one rule. The pointer is added only where cobra does not print it
itself, which is decided by `SilenceErrors`.

Once per 24 hours, if a newer release exists, one line goes to stderr **after**
your command's own output:

```text
tool v1.4.0 available (running v1.3.2) — run `tool update`
```

The check runs concurrently with your command and is abandoned if the command
finishes first, so a fast command pays nothing. It never returns an error and
never prints one: a failed check is recorded in the state file and swallowed,
because an update notice must not be able to break the command the user typed.

Nothing is printed when any of these hold:

| Condition | Why |
| --- | --- |
| `NO_AUTO_UPDATE` or `TOOL_NO_AUTO_UPDATE` is set | Opted out |
| `CI`, `BUILD_NUMBER`, `RUN_ID`, `GITHUB_ACTIONS`, `CODESPACES` | Not a human |
| stdout or stderr is not a terminal | `tool list > out 2>&1` must stay clean |
| The version is not a plain `vX.Y.Z` | A development build |
| The command is `update`, `version`, `completion` or a shell-completion callback | Would be pointless or constant |
| Checked within the interval | One request per day, not per invocation |

Presence-only, any value: `NO_AUTO_UPDATE=0` disables it, the same way
[`NO_COLOR`](https://no-color.org) works. Set the interval separately with
`AUTO_UPDATE_INTERVAL=6h` or `TOOL_AUTO_UPDATE_INTERVAL=30m`.

`Config.Interactive` overrides terminal detection — pass `false` from a program
that already knows it is writing into a pager or a structured-output mode.

State lives in
`${XDG_STATE_HOME:-~/.local/state}/<tool>/autoupdate-<machine>.json`, written
atomically, and the timestamp is written **before** the network call — `gh`
stamps only on success, so a rate-limited or offline user re-hits the API on
every invocation until the window resets.

`<machine>` is the bare lowercased hostname, and it is in the name because both
fields the file carries — the version installed here, and the instant this box
last checked — describe one machine. A state directory that is shared between
machines, by a file syncer or a network home directory, otherwise has two
writers on one path and reports whichever wrote last as the state of all of
them. `autoupdate.Machine` returns the name this process writes under.

`autoupdate` links nothing outside the standard library, so a CLI using `flag`,
`urfave/cli` or its own parser can take the notice without taking cobra.

## Scope

Provided: GitHub releases and checksum verification. GitLab, Gitea and
signature verification are not implemented — [`Source`] and [`Verifier`] are
the interfaces to implement for them, and neither requires changes here.

Not supported: in-place binary patching, rollback to an arbitrary version, and
update channels beyond the prerelease toggle.

## Alternatives

| Package | Notes |
| --- | --- |
| [creativeprojects/go-selfupdate] | Broadest source support (GitHub, GitLab, Gitea, HTTP). Links `x/crypto/openpgp` for signature verification, so it carries [GO-2026-5932]; pulls in ~14 modules |
| [minio/selfupdate] | Applies an update with minisign verification. Does not locate releases — that half is yours to write |
| [sanbornm/go-selfupdate] | The original. Binary-diff patching against your own update server |

Use this one if you publish goreleaser archives to GitHub releases and want no
dependencies. Use `creativeprojects/go-selfupdate` if you need GitLab or Gitea.

## Requirements

- The Go version in [go.mod](go.mod), or newer.
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

[cobra]: https://github.com/spf13/cobra
