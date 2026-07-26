# goselfupdate — Claude Code instructions

A public Go library, not a CLI. There is no `main` package and nothing to
install; it ships as git tags and is consumed by the Go tools in `~/tools/`.

Unlike the other repos here, this one is written to be used by strangers. Treat
the exported API, the README and the godoc as the product — a change that is
merely convenient for the three internal consumers is not automatically right.

## Layout

| Path | Holds |
|---|---|
| root package | The library. Standard library imports only |
| `cobracmd/` | The cobra `update` command. The only package importing cobra |
| `semver.go` | Version comparison, replacing `x/mod/semver` |

## Constraints that must not regress

- **The core package has zero third-party dependencies.** CI enforces it. This
  is the library's main differentiator against the alternatives, and the reason
  `semver.go` exists rather than importing `x/mod/semver`, which raises its
  minimum Go version over time and would push that floor onto every caller.
- **cobra stays confined to `cobracmd/`.** Module graph pruning is what keeps a
  core-only consumer from downloading it, and that only holds while the core
  imports nothing.
- **The Go floor is 1.23 and CI tests against it.** Raising it excludes callers
  and needs a reason beyond convenience.
- **`x/crypto/openpgp` is never linked.** Avoiding [GO-2026-5932] is the
  original reason this library exists.
- **Verification precedes extraction**, so no unverified bytes reach an archive
  reader or the disk.
- **Assets are discovered from the release API, never rebuilt from a name
  template**, and an ambiguous match is an error rather than a guess.
- **Errors are sentinels.** A new failure mode gets an `Err*` in `errors.go`;
  callers must never have to match on message text.

## Testing

Everything runs offline. `stubSource` serves fixed releases; `github_test.go`
drives the real HTTP path against `httptest`.

`Update` resolves the *running* executable, which under test is the test
binary, so tests call `UpdateTo` with an explicit path and the cobra command is
exercised through `--check`. Never write a test that calls `Update` directly —
it would overwrite the test binary.

`FuzzParseVersion` guards the hand-written version parser; CI runs it for 60
seconds per build. `TestCompareVersionFollowsSpecPrecedence` asserts the full
ordering matrix from semver.org section 11, which is the authority the
implementation was written against.

The Windows install path cannot run here. CI cross-compiles and vets every
supported platform, and runs the suite on a Windows runner.

## Releasing

Tag `vX.Y.Z` on main; the release workflow validates on three operating systems
before publishing. A tag cannot be retracted once the module proxy caches it,
only superseded — so a broken tag is permanent. Update `CHANGELOG.md` in the
same commit.

After tagging, bump the consumers: `go get -u github.com/datapointchris/goselfupdate`
in `todoui`, `toolbox` and `forge`. All three are on v0.1.0 and released with
it (todoui v1.6.2, toolbox v1.7.1, forge v1.13.2).

[GO-2026-5932]: https://pkg.go.dev/vuln/GO-2026-5932
