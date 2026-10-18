# goselfupdate

A Go library that gives a CLI its `update` command and its once-a-day notice
that a newer release exists. It reads releases from GitHub, verifies the
download against the release's checksum file, and swaps the running executable
in one rename.

It has two packages. The root package installs a release, and backs the
`update` command a user types. `autoupdate` only tells: it checks in the
background and prints one line, and it never installs. A program wanting only
the install half links none of the notice.

Neither package knows what a command-line parser is, and both import only the
standard library. So it works the same behind `flag`, `urfave/cli`, a
hand-rolled parser or [cobra]. For cobra, [goclikit] is the front door. It
builds the `update` subcommand from this library and runs the background check
around every other command.

## Install

```bash
go get github.com/datapointchris/goselfupdate@latest
```

It needs three things:

- The Go version in [go.mod](go.mod), or newer.
- Releases that publish per-platform archives and a checksums file.
  [goreleaser]'s defaults produce both.
- A version injected at build time, as in
  `-ldflags "-X main.version={{.Version}}"`. goreleaser's default ldflags
  already set `main.version`.

## Starting from scratch

One `main` carries both halves. The `update` branch installs, and every other
invocation runs beside the background check.

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/datapointchris/goselfupdate"
    "github.com/datapointchris/goselfupdate/autoupdate"
)

var version = "dev" // a release build overwrites this through -ldflags

func main() {
    _ = goselfupdate.CleanupOldBinary() // removes what a Windows update displaced; a no-op elsewhere

    ctx := context.Background()
    release := goselfupdate.Config{
        Owner:   "you", // the GitHub repository that publishes the releases
        Repo:    "tool",
        Binary:  "tool", // the executable's name inside each release archive
        Version: version,
    }

    if len(os.Args) > 1 && os.Args[1] == "update" {
        result, err := goselfupdate.Update(ctx, release) // replaces this executable on disk
        if err != nil {
            fmt.Fprintln(os.Stderr, err)
            os.Exit(1)
        }
        fmt.Printf("%s → %s, applied: %v\n", result.From, result.To, result.Applied)
        return // update never starts the background check
    }

    session := autoupdate.Start(ctx, autoupdate.Config{Update: release}) // returns at once
    err := run(os.Args[1:])                                              // the tool's own work
    session.Finish()                                                     // prints the notice, if any
    if err != nil {
        os.Exit(1)
    }
}
```

Once the repository has a published release, build it and run it:

```bash
go build -o tool .                                    # version stays "dev"
./tool update                                         # refuses: cannot update a development build
go build -ldflags "-X main.version=v0.0.1" -o tool .  # stands in for an old release
./tool                                                # in a terminal: one line naming the newer release
./tool update                                         # downloads, verifies and swaps ./tool in place
./tool update                                         # proves it: From and To match, applied: false
```

**A version that is not semver refuses.** `Update` returns `ErrDevBuild` for
an empty version, `dev`, or anything else that does not parse as semver. It
returns before any request is made. There is nothing meaningful to compare, and
an update would silently discard whatever local build was in place.

The background check is stricter. It also skips a pre-release, and a Go
pseudo-version such as `v1.6.1-0.20260724161156-2c04703+dirty`, which is valid
semver. `goselfupdate.IsReleaseVersion` is that stricter test. A caller that
wants `update` to refuse those builds too checks it before calling `Update`.

**Windows cannot overwrite a running executable.** There `Update` renames the
running binary to `<name>.old` and moves the release into its place. The old
copy cannot be deleted while the process runs, so `CleanupOldBinary` removes it
on the next start. It reports no error when there is nothing to remove, so it is
safe on every platform and every start.

**No secret is needed.** The GitHub source authenticates with whatever
`gh auth token` prints, and sends the request anonymously when that fails. An
anonymous request works against a public repository. A private one needs a
token from one of the four sources under Authentication below, and a logged-in
`gh` is one of them.

## `Update` — find the asset by name, verify it, then swap the binary in one rename

```go
goselfupdate.Check(ctx, config)                             // is a newer release out? downloads and writes nothing
goselfupdate.Update(ctx, config)                            // installs it over the running executable
goselfupdate.UpdateTo(ctx, config, path)                    // installs it over the binary at path
goselfupdate.Changelog(ctx, config, result.From, result.To) // the commit subjects between two versions
goselfupdate.CleanupOldBinary()                             // removes what a Windows update displaced
```

`Update` runs five steps and stops at the first failure:

1. Reads the latest release from the GitHub API.
2. Compares it with the running version by semantic version precedence. A
   leading `v` is optional on both.
3. Picks the asset that names the running `GOOS` and `GOARCH`.
4. Verifies its SHA-256 against the release's checksums file.
5. Extracts the binary and replaces the running executable.

**Assets are discovered, not reconstructed.** Asset names come from the API and
are matched on delimited OS and architecture words. So projects naming archives
`tool_1.0.0_darwin_arm64.tar.gz` or `tool-1.0.0-macos-aarch64.zip` both work,
and a project that changes its naming does not silently break. An ambiguous
match is an error listing the candidates, never a guess. Picking wrong means
overwriting a working binary with one built for another architecture.

**Verification precedes extraction.** Nothing unverified is passed to an
archive reader or written to disk.

**Replacement is atomic.** The release's binary is staged in the target's own
directory, flushed to disk, and renamed into place. An interrupted update
cannot leave a half-written executable. On Unix a running process holds its
executable by inode, so the rename is safe mid-execution. Symbolic links are
resolved first, so an update rewrites the real file rather than replacing a
link with a binary.

**`Changelog` asks GitHub's compare endpoint** for the commits between two tags
and returns each one's subject line. A custom `Source` that does not implement
`Changeloger` returns none.

Everything beyond `Owner`, `Repo`, `Binary` and `Version` has a working
default. `HTTPClient` defaults to a 60-second timeout. `AllowPrerelease`
includes prereleases when selecting the newest version. `Source` and `Verifier`
are the extension points, covered under Scope. GitHub Enterprise works by
passing a `GitHubSource` whose `APIBase` is its `/api/v3` root as `Source`. A
hand-built source carries its own `HTTPClient`, `TagPrefix` and token settings,
and the matching `Config` fields go unused.

### Supported release layouts

| Aspect | Supported |
| --- | --- |
| Archives | `.tar.gz`, `.tgz`, `.zip`, and bare uncompressed binaries |
| Platforms | Linux, macOS, Windows, and other Unix targets |
| Naming | `_` or `-` separated; `x86_64`/`amd64`, `aarch64`/`arm64`, `macos`/`darwin` |
| macOS | Universal (`darwin_all`) binaries, used only when no native asset exists |
| Nesting | Binary at the archive root or in a subdirectory; `.exe` tolerated |
| Checksums | One asset whose name contains `checksum`, in `sha256sum` format, with or without the binary-mode `*` or a leading path |
| Tags | `v1.2.3` or `1.2.3`, or `cli/v1.2.3` in a repository publishing several components |

goreleaser's defaults satisfy all of this with no configuration. Its split
checksum mode, one `.sha256` file per artifact, is not read.

### Repositories publishing more than one component

A repository that releases several things gives each its own tag prefix, such
as `cli/v1.2.3` and `api/v2.0.0`. Go requires it of a module in a subdirectory,
and goreleaser calls it a monorepo tag prefix. Set `TagPrefix` to pick a stream:

```go
goselfupdate.Config{
    Owner:     "you",
    Repo:      "project",
    Binary:    "tool",
    Version:   version,
    TagPrefix: "cli/",
}
```

This is not only a parsing convenience. GitHub's "latest release" endpoint is
repository-wide, so without a prefix it returns whichever component released
most recently. A CLI would eventually try to install its own application's
release. A prefix switches to the release list, filters it, and picks the
highest version rather than the most recently created. So a patch to an older
line, published after a newer minor, is not offered as an update. The search
reads one page of the list at the API's maximum page size, so a component whose
newest release sits deeper than that is reported as `ErrNoRelease`.

Tags come back with the prefix removed. `Release.Tag`, `Result.From` and
`Result.To` are versions, and the repository's tag layout stays internal.

## Authentication — on by default, resolved once by the GitHub source

**Configure nothing.** `GitHubSource` runs `gh auth token` when its first
request is about to be made, and sends what it prints. The credential is
resolved once and reused for the source's lifetime.

The alternative is not "no credential". It is 60 requests an hour, charged
**per IP address** and shared with every other anonymous caller behind the same
egress. A default that has to be opted into is a default nobody sets.

Four sources, first non-empty wins:

| Source | Set by | Default |
| --- | --- | --- |
| `Config.Token` | you, in code | unset |
| `$GITHUB_TOKEN`, then `$GH_TOKEN` | whoever runs your CLI | unset |
| `TokenFunc()` | you, in code | unset |
| `$GITHUB_TOKEN_COMMAND` | whoever runs your CLI | `gh auth token` |

`TokenFunc` is called only when a request is about to be made. A credential
that costs a keychain prompt or a subprocess is paid for only on the
invocations that reach the network.

`$GITHUB_TOKEN_COMMAND` both redirects and disables, which is what a switch has
to do to be worth having:

```bash
GITHUB_TOKEN_COMMAND='pass show github/token'   # use this instead
GITHUB_TOKEN_COMMAND=''                         # run nothing, stay anonymous
```

Single and double quotes are honored. No shell runs the result, so pipelines
and expansion are not available. It never fails. A command that is not
installed, exits non-zero, or takes longer than ten seconds degrades to an
unauthenticated request.

**This lives on `GitHubSource`, not on `Config`.** A credential is the host's
business. A `Source` for GitLab or Gitea brings its own variable and its own
command, and nothing above the `Source` interface learns either name.

## `autoupdate` — a daily check that prints one line and never installs

```go
session := autoupdate.Start(ctx, config)    // runs the gate; if it passes, starts the check and returns
outcome := session.Finish()                 // waits for the check, bounded by Timeout, then prints
enabled, skip := autoupdate.Enabled(config) // whether a check would run now, and why not
path := autoupdate.StatePath("tool")        // where this machine's state for the tool lives
```

At most once per interval, a day by default, the check asks for the latest
release. If it is newer, one line goes to stderr **after** your command's own
output:

```text
tool v1.4.0 available (running v1.3.2) — run `tool update`
```

`Start` returns at once and the check runs beside the command. A command that
outlasts the check pays nothing for it. One that finishes first waits in
`Finish`, at most `Config.Timeout`, which is five seconds by default. Then the
check is abandoned. Within the interval the gate declines before any request,
and its only I/O is reading the state file.

It never returns an error and never prints one. A failed check is recorded in
the state file and swallowed, because an update notice must not be able to
break the command the user typed.

The check is skipped, and nothing is printed, when any of these hold. The gate
tests them in this order:

| Condition | Why |
| --- | --- |
| `Config.Suppress` is true | The caller marked a command that must never check, such as an update command or a shell-completion callback |
| `NO_AUTO_UPDATE` or `<TOOL>_NO_AUTO_UPDATE` is set | Opted out |
| The version is not a plain `X.Y.Z`, with or without the `v` | A development build, a pre-release or a Go pseudo-version |
| stdout or stderr is not a terminal | `tool list > out 2>&1` must stay clean |
| `CI`, `BUILD_NUMBER`, `RUN_ID`, `GITHUB_ACTIONS` or `CODESPACES` is set | Not a human |
| Checked within the interval | One request per interval, not per invocation |

Every variable in the table counts when it is present, whatever its value.
`NO_AUTO_UPDATE=0` disables the check, the same way
[`NO_COLOR`](https://no-color.org) works. `<TOOL>` is `Config.Tool`, which
defaults to `Binary`, uppercased with `-` turned into `_`. A tool named
`my-tool` reads `MY_TOOL_NO_AUTO_UPDATE`.

The interval is `Config.Interval`, else `<TOOL>_AUTO_UPDATE_INTERVAL`, else
`AUTO_UPDATE_INTERVAL`, else 24 hours. Both variables take `30s`, `45m`, `12h`,
`7d` or a bare number of seconds. A value that does not parse is ignored.

`Config.Interactive` overrides terminal detection. Pass `false` from a program
that already knows it is writing into a pager or a structured-output mode.

State lives in
`${XDG_STATE_HOME:-~/.local/state}/<tool>/autoupdate-<machine>.json`, and
`Config.StateDir` overrides the root. It is written atomically with mode 0600.
The timestamp is written **before** the network call. `gh` stamps only on
success, so a rate-limited or offline user re-hits the API on every invocation
until the window resets. A check the gate declines writes nothing, so a missing
state file shows the network was never touched.

`<machine>` is the bare lowercased hostname, with any domain dropped, or
`unknown` when the hostname cannot be read. It is in the name because both
fields the file carries describe one machine: the version installed here, and
the instant this box last checked. A state directory shared between machines,
by a file syncer or a network home directory, otherwise has two writers on one
path. It then reports whichever wrote last as the state of all of them.
`autoupdate.Machine` returns the name this process writes under.

The Python and bash siblings, [pyselfupdate] and [bashselfupdate], write the
same schema under the same name. One reader can glob
`~/.local/state/*/autoupdate-*.json` and read every tool's state.

## With cobra — goclikit carries the command and the suppression list

```go
root.AddCommand(goclikit.UpdateCommand(release))                       // adds `tool update` and `tool update --check`
err := goclikit.Execute(ctx, root, autoupdate.Config{Update: release}) // runs root beside the background check
```

```console
$ tool update --check
✓ tool update available: v1.6.0 → v1.6.1

$ tool update
✓ tool updated: v1.6.0 → v1.6.1

Changes:
  • fix(download): retry a transfer the server cut short
  • docs: describe the config file's search order
```

`Execute` sets `Suppress` for the update command under whatever name it was
given, and for `version`, `completion` and its subcommands, `help`, cobra's
shell-completion callbacks, `--help`, and a command line cobra cannot resolve.
The completion callback is the one that matters most: it runs on every TAB
press.

The command takes no alias. A second verb such as `upgrade` beside `update`
leaves its reader guessing which one is real.

## Errors — one sentinel per failure mode, so callers branch with `errors.Is`

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

The full set is `ErrDevBuild`, `ErrNoRelease`, `ErrNoAsset`,
`ErrAmbiguousAsset`, `ErrNoChecksums`, `ErrChecksumMismatch`,
`ErrBinaryNotFound` and `ErrInvalidConfig`.

## Security — checksums catch a bad download, not a compromised account

Integrity comes from the release's checksum file, fetched over TLS from the
same release. This defends against a corrupted, truncated or intercepted
download. It does **not** defend against a compromised publishing account,
which can rewrite the checksum file alongside the asset. That requires a
signature verified against a key distributed out of band. Implement
[`Verifier`] to add one.

Only an archive entry's base name is compared, and archive paths never build an
output path. An entry naming `../../etc/passwd` has nothing to escape into.

This package does not link `golang.org/x/crypto/openpgp`. That package is
unmaintained and carries a permanent advisory, [GO-2026-5932], with no fixed
version. A project using this library inherits no advisory from it.

## Scope — GitHub and checksums built in, everything else behind two interfaces

Provided: GitHub releases and checksum verification. GitLab, Gitea and
signature verification are not implemented. [`Source`] and [`Verifier`] are
the interfaces to implement for them, and neither requires changes here.

Not supported: in-place binary patching, rollback to an arbitrary version, and
update channels beyond the prerelease toggle.

## Alternatives — another library for GitLab, Gitea, minisign or binary patching

| Package | Notes |
| --- | --- |
| [creativeprojects/go-selfupdate] | Broadest source support: GitHub, GitLab, Gitea and HTTP. Links `x/crypto/openpgp` for signature verification, so it carries [GO-2026-5932] |
| [minio/selfupdate] | Applies an update with minisign verification. Does not locate releases; that half is yours to write |
| [sanbornm/go-selfupdate] | Binary-diff patching against your own update server |

Use this one to install goreleaser archives from GitHub releases with nothing
beyond the standard library. Use `creativeprojects/go-selfupdate` for GitLab or
Gitea.

## License

MIT

[goreleaser]: https://goreleaser.com
[GO-2026-5932]: https://pkg.go.dev/vuln/GO-2026-5932
[creativeprojects/go-selfupdate]: https://github.com/creativeprojects/go-selfupdate
[minio/selfupdate]: https://github.com/minio/selfupdate
[sanbornm/go-selfupdate]: https://github.com/sanbornm/go-selfupdate
[`Verifier`]: https://pkg.go.dev/github.com/datapointchris/goselfupdate#Verifier
[`Source`]: https://pkg.go.dev/github.com/datapointchris/goselfupdate#Source
[cobra]: https://github.com/spf13/cobra
[goclikit]: https://github.com/datapointchris/goclikit
[pyselfupdate]: https://github.com/datapointchris/pyselfupdate
[bashselfupdate]: https://github.com/datapointchris/bashselfupdate
