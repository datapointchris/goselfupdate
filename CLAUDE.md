# goselfupdate — Claude Code instructions

A public Go library, not a CLI. There is no `main` package and nothing to
install; it ships as git tags and is consumed by the Go tools in `~/tools/`.

This one is written to be used by strangers. Treat the exported API, the README
and the godoc as the product — a change that is merely convenient for the internal
consumers is not automatically right. `go list -m all` names them; there are more
than the handful this line used to claim.

## Layout

| Path | Holds |
| --- | --- |
| root package | The library. Standard library imports only |
| `autoupdate/` | The notify layer: gate, interval, state file. Also stdlib-only |
| `cobracmd/` | The cobra `update` command and `Execute`. The only package importing cobra |
| `semver.go` | Version comparison, replacing `x/mod/semver` |

## Constraints that must not regress

- **The core package *and* `autoupdate/` have zero third-party dependencies.**
  CI enforces both. This is the library's main differentiator against the
  alternatives, and the reason `semver.go` exists rather than importing
  `x/mod/semver`, which raises its minimum Go version over time and would push
  that floor onto every caller. `autoupdate` hand-rolls terminal detection
  (`os.Stat` + `ModeCharDevice`, not `x/term`) and duration parsing
  (`time.ParseDuration` rejects `7d`) for the same reason.
- **cobra stays confined to `cobracmd/`.** Module graph pruning is what keeps a
  core-only consumer from downloading it, and that only holds while the core
  imports nothing.
- **`go.mod` declares the Go floor and CI tests against it**, reading the version
  from the file rather than repeating it. Raising the floor excludes callers and
  needs a reason beyond convenience.
- **`x/crypto/openpgp` is never linked.** Avoiding [GO-2026-5932] is the
  original reason this library exists.
- **Verification precedes extraction**, so no unverified bytes reach an archive
  reader or the disk.
- **Assets are discovered from the release API, never rebuilt from a name
  template**, and an ambiguous match is an error rather than a guess.
- **Errors are sentinels** — an `Err*` in `errors.go` per failure mode. The rule
  is `standards/code-quality.md` § "A failure mode gets a name callers can branch
  on, never a message they have to match", which names this file as its source.
- The gate order, the silent notify path, the pre-flight timestamp stamp and the
  shared `autoupdate.json` schema are `standards/release.md` § Self-update. Not
  restated here, as in bashselfupdate and pyselfupdate.
- **The stamp rule caught a real bug here**, which is the part the standard does
  not carry: the first implementation stamped a *copy*, and the write after the
  check clobbered the timestamp back to zero. The test is named for it.

## Why IsReleaseVersion exists separately from IsValidVersion

Why a dev build cannot be detected from a version string is `standards/release.md`
§ "Never detect a dev build from a version string" — both example strings and the
maxim are there.

What is this repo's own: a caller asking "is this a real release" therefore cannot
use `IsValidVersion`, and every consumer had independently reimplemented the same
`^v\d+\.\d+\.\d+$` regex to get the right answer. Exporting both predicates is what
stopped that, and it is why two functions that look redundant are not.

## Testing

The two-layer shape — `stubSource` for the logic, `github_test.go` against
`httptest` for the wire — is `standards/testing.md` § "A network client tests
offline against a stub, plus one test against a local server", which cites this
file. Everything runs offline.

`Update` resolves the *running* executable, which under test is the test
binary, so tests call `UpdateTo` with an explicit path and the cobra command is
exercised through `--check`. Never write a test that calls `Update` directly —
it would overwrite the test binary.

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

`allow-initial-development-versions` holds this on 0.x — why, and which three repos
shipped 1.0.0 as their first automated release without it, is `standards/release.md`
§ "On 0.x, go-semantic-release majors on *everything* unless told not to". Drop that
input when the API is settled enough to promise compatibility.

The changelog is the GitHub release body, written by go-semantic-release from
the commit subjects in the release. Nothing commits it back to the repo, so the
commit message is where a change explains itself to a consumer.

Then bump the consumers, which are whatever declares the module rather than a
list that goes stale here:

```bash
rg -l datapointchris/goselfupdate ~/tools/*/go.mod ~/webapps/*/cli/go.mod
```

[GO-2026-5932]: https://pkg.go.dev/vuln/GO-2026-5932

## Never write the breaking-change trailer in a commit message

The words `BREAKING CHANGE` — either number, colon or not, subject or body — cut a major release
here, and a major on this repo is an outage rather than a version. `commit-analyzer-cz` matches
them unanchored against the raw message and ORs the result with the configured major rules, so
`.semrelrc` cannot stop it and it majors even a `fix:` commit.

The module path carries no `/vN` suffix, so once a major exists `go install …@latest` cannot see it
and silently resolves the highest v1 instead — `dotfiles check` reports the tool stale forever
while `apply` exits 0 having installed nothing. Every already-installed binary is stranded too:
`goselfupdate` refuses a lower version and reports "already up to date". Recovery is a reinstall on
each machine, and it is not a rewrite — branch protection refuses one on `main`, and the offending
commit re-cuts the major on every push until a tag above it takes it out of range.

**The ban covers a commit that merely discusses the trailer.** One explaining this exact caveat cut
a fresh major on push. Name it some other way — "that marker" — and never quote it.

Deliberate majors use `chore(release-major)`, the one subject `.semrelrc` leaves as a major. Full
reasoning and the reset procedure: `standards/release.md` § "Never write the breaking-change
trailer in a Go repo's commit message".
