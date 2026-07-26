// Package goselfupdate replaces a running binary with a newer release.
//
// It targets the shape of release that [goreleaser] produces by default:
// per-platform archives published to a GitHub release alongside a
// checksums.txt. Integrity is established from that checksum file, so no
// signing key or PGP implementation is involved.
//
// # Usage
//
// The zero-configuration case needs four fields:
//
//	result, err := goselfupdate.Update(ctx, goselfupdate.Config{
//		Owner:   "datapointchris",
//		Repo:    "todoui",
//		Binary:  "todoui",
//		Version: version, // injected at build time via ldflags
//	})
//	if err != nil {
//		return err
//	}
//	if result.Applied {
//		fmt.Printf("updated %s → %s\n", result.From, result.To)
//	}
//
// [Check] performs the same lookup without downloading or writing anything.
//
// For a ready-made cobra command, see the cobracmd subpackage. It is kept
// separate so that importing this package does not pull in a CLI framework.
//
// # Scope
//
// GitHub releases and checksum verification are provided. [Source] and
// [Verifier] are the extension points for anything else — a different forge,
// or signature verification — and can be supplied through [Config] without
// changes here.
//
// Archives may be tar.gz or zip; a release asset that is a bare binary is also
// accepted. Linux, macOS and Windows are supported.
//
// # Versions
//
// Versions are compared with [golang.org/x/mod/semver]. A leading "v" is
// optional on both the running version and the release tag, since goreleaser
// configurations disagree about whether to inject {{.Tag}} or {{.Version}}.
//
// A binary with no version injected reports itself as a development build and
// refuses to update, because there is no meaningful version to compare and an
// update would silently discard whatever local build was in place.
//
// [goreleaser]: https://goreleaser.com
package goselfupdate
