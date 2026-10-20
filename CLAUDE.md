# goselfupdate — Claude Code instructions

A public Go library, not a CLI. There is no `main` package and nothing to
install; it ships as git tags and Go CLIs import it.

It knows nothing about command-line parsing. goclikit
(`github.com/datapointchris/goclikit`) is the cobra front door — the `update`
subcommand, and the version check racing alongside whatever else was typed —
and it imports this.

This one is written to be used by strangers. Treat the exported API, the README
and the godoc as the product — a change that is merely convenient for the
author's own consumers is not automatically right. The command under Releasing
finds them.

## Layout

| Path | Holds |
| --- | --- |
| root package | The library. Standard library imports only |
| `autoupdate/` | The notify layer: gate, interval, state file. Also stdlib-only |
| `semver.go` | Version comparison, replacing `x/mod/semver` |

## Constraints that must not regress

- **`go.mod` declares the Go floor and CI tests against it**, reading the version
  from the file rather than repeating it. Raising the floor excludes callers and
  needs a reason beyond convenience.
- **`x/crypto/openpgp` is never linked.** Avoiding [GO-2026-5932] is the
  original reason this library exists.
- **Verification precedes extraction**, so no unverified bytes reach an archive
  reader or the disk.
- **Assets are discovered from the release API, never rebuilt from a name
  template**, and an ambiguous match is an error rather than a guess.
- **Errors are sentinels** — an `Err*` in `errors.go` per failure mode. A
  failure mode gets a name callers can branch on with `errors.Is`, never a
  message they have to match.
- **The notify layer's contract is shared with bashselfupdate and
  pyselfupdate**, so a change to it lands in all three or in none:
  - The gate is all local and runs in a fixed order: suppression, the
    environment kill switch, a dev build, both streams being terminals, CI,
    then the interval.
  - The notify path never prints an error. A failure goes to the state file
    and is swallowed; errors surface only in `<tool> update`.
  - `checked_at` is stamped before the network call. `gh` stamps only on
    success, so a rate-limited or offline user re-hits the API on every
    invocation.
  - The `autoupdate-<machine>.json` schema is read across languages with no
    version negotiation. Adding a field is safe; renaming or repurposing one
    breaks the other two.
- **The machine in the state filename is derived identically in all three
  siblings** — bare hostname, domain dropped, lowercased, `unknown` when it
  cannot be read. A library deriving it differently puts one box's state under
  two names, and neither box then sees the other's.
- **`Start` stamps the state value it later rewrites, never a copy of it.**
  Stamping a copy let the write after the check clobber the timestamp back to
  zero. `TestTheTimestampIsWrittenBeforeTheCheckNotAfter` is the test for it.

## Dependencies: one import is banned, not all of them

`x/crypto/openpgp` is the ban, and it is above with the reason. Nothing else is
forbidden. Weigh a third-party import here the way you would in any repo — what
it costs to audit, what it does to the Go floor, whether the stdlib already
answers it.

The packages here happen to import nothing outside the standard library, and
that is a consequence rather than a promise. Two pieces of hand-rolled code
exist because the alternative was worse on its own merits, and both stay:

- `semver.go` rather than `x/mod/semver`, which raises its minimum Go version
  over time. That floor lands on every caller, and the code it replaces is a
  comparison function pinned by `FuzzParseVersion` and by the full ordering
  matrix from semver.org section 11.
- `autoupdate`'s terminal detection (`os.Stat` plus `ModeCharDevice`, not
  `x/term`) and its duration parsing, because `time.ParseDuration` rejects
  `7d`. Both are a few lines against a package each.

Neither is a reason to refuse an import that genuinely pays. A dependency that
would carry real weight — a protocol client, a compression format, a parser —
is a normal decision made on what it costs, and "this package has none" is not
an argument against it.

## Why IsReleaseVersion exists separately from IsValidVersion

A dev build cannot be detected by parsing its version string. A Go
pseudo-version, `v1.6.1-0.20260724161156-2c04703+dirty`, and a `git describe`
string, `v1.2.3-4-gabc1234`, are both valid semver: they parse and sort
correctly.

So a caller asking "is this a real release" cannot use `IsValidVersion`, and every
consumer had independently reimplemented the same `^v\d+\.\d+\.\d+$` regex to get
the right answer. Exporting both predicates is what stopped that, and it is why two
functions that look redundant are not.

## Testing

Two layers, and both are needed: `stubSource` for the logic, and
`github_test.go` against an `httptest` server for the wire. The stub proves the
decisions and cannot prove the wire. Everything runs offline.

`Update` resolves the *running* executable, which under test is the test
binary, so tests call `UpdateTo` with an explicit path. Never write a test that
calls `Update` directly — it would overwrite the test binary.

`autoupdate` is tested through a `stubSource` that counts how often it was
asked. Most of those tests assert that *nothing* happened, so the call count and
the absence of a state file are the real assertions — a skip that still reached
the network is a skip that failed. Terminal detection is injected via
`Config.Interactive` rather than faked, because a test process has no terminal
and reassigning `os.Stdout` would not survive.

`FuzzParseVersion` guards the hand-written version parser; CI runs it for 60
seconds per build. `TestCompareVersionFollowsSpecPrecedence` asserts the full
ordering matrix from semver.org section 11, which is the authority the
implementation was written against.

The Windows install path cannot run here. CI cross-compiles and vets every
supported platform, and runs the suite on a Windows runner.

## Releasing

Push to main. The workflow validates on three operating systems and then tags,
so the conventional-commit type is what picks the version. A tag cannot be
retracted once the module proxy caches it, only superseded — which is why the
checks run before the tag exists rather than after it, as they did while
releases were cut by hand.

`allow-initial-development-versions` holds this on 0.x. Without it,
go-semantic-release treats any change on a 0.x repo as a major, so a bare `fix:`
ships 1.0.0 as the first automated release. With it, a major change becomes a
minor bump and the major stays 0. Drop that input when the API is settled
enough to promise compatibility, because 1.0 is that promise.

The changelog is the GitHub release body, written by go-semantic-release from
the commit subjects in the release. Nothing commits it back to the repo, so the
commit message is where a change explains itself to a consumer.

Then bump the consumers, which are whatever declares the module rather than a
list that goes stale here:

```bash
rg -l --glob go.mod datapointchris/goselfupdate <directory holding your checkouts>
```

[GO-2026-5932]: https://pkg.go.dev/vuln/GO-2026-5932

## Never write the breaking-change trailer in a commit message

The words `BREAKING CHANGE` — either number, colon or not, subject or body — cut a major release
once this repo is past 0.x, and a major from v1 on is an outage rather than a version. On 0.x,
`allow-initial-development-versions` cuts them as a minor. `commit-analyzer-cz` matches them
unanchored against the raw message and ORs the result with the configured major rules, so
`.semrelrc` cannot stop it and it majors even a `fix:` commit.

The module path carries no `/vN` suffix, so once a major exists `go install …@latest` cannot see it
and silently resolves the highest v1 instead — an installer comparing against the latest tag
reports the tool stale forever while installing nothing. Every already-installed binary is stranded
too: `goselfupdate` refuses a lower version and reports "already up to date". Recovery is a
reinstall on each machine, and it is not a rewrite — branch protection refuses one on `main`, and
the offending commit re-cuts the major on every push until a tag above it takes it out of range.

**The ban covers a commit that merely discusses the trailer.** One explaining this exact caveat cut
a fresh major on push. Name it some other way — "that marker" — and never quote it.

This repo carries no `.semrelrc`, so no commit subject maps to a major. Reaching 1.0 is dropping
`allow-initial-development-versions`, and v1 needs no path suffix. A deliberate v2 or later needs
a `.semrelrc` mapping `chore(release-major)` to a major. The same release adds `/v2` to the module
path, so `go install <module>/v2@latest` resolves the new tag. Without the suffix, that major is
the outage above.
