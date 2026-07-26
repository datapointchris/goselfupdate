package goselfupdate

import (
	"fmt"
	"net/http"
	"time"
)

// DefaultTimeout bounds an update when Config.HTTPClient is not supplied.
const DefaultTimeout = 60 * time.Second

// Config describes what to update to and how to reach it.
//
// Owner, Repo, Binary and Version are required unless a custom Source is
// supplied, in which case Owner and Repo are unused. Every other field has a
// working default.
type Config struct {
	// Owner and Repo identify the GitHub repository publishing the releases.
	Owner string
	Repo  string

	// Binary is the executable's name inside the release archive. It is also
	// used in messages produced by the cobracmd subpackage.
	Binary string

	// Version is the running build's version, conventionally injected with
	// -ldflags "-X main.version=...". A leading "v" is optional. An empty,
	// "dev" or otherwise non-semver value makes the update fail with
	// [ErrDevBuild].
	Version string

	// Token authenticates requests to the release source. Without one, GitHub
	// allows 60 API requests per hour per IP address and rejects private
	// repositories outright. Defaults to $GITHUB_TOKEN, then $GH_TOKEN.
	Token string

	// HTTPClient performs every request. Defaults to a client with
	// [DefaultTimeout]. Supply one to control proxies, retries or timeouts.
	HTTPClient *http.Client

	// Source locates releases. Defaults to a [GitHubSource] built from Owner,
	// Repo, Token and HTTPClient.
	Source Source

	// Verifier checks a downloaded asset before it is extracted. Defaults to
	// [ChecksumVerifier], which uses the release's own checksums file. Set
	// [NoVerification] to skip the check, which is not recommended.
	Verifier Verifier

	// AllowPrerelease considers prereleases when looking for the latest
	// version. GitHub excludes them from its "latest release" endpoint, so
	// enabling this changes which endpoint is used.
	AllowPrerelease bool
}

func (c *Config) validate() error {
	if c.Binary == "" {
		return fmt.Errorf("%w: Binary is required", ErrInvalidConfig)
	}
	if c.Source == nil {
		if c.Owner == "" || c.Repo == "" {
			return fmt.Errorf("%w: Owner and Repo are required without a custom Source", ErrInvalidConfig)
		}
	}
	return nil
}

// resolve fills in the defaults so the rest of the package can assume every
// field is populated.
func (c Config) resolve() (Config, error) {
	if err := c.validate(); err != nil {
		return c, err
	}

	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: DefaultTimeout}
	}
	if c.Token == "" {
		c.Token = tokenFromEnv()
	}
	if c.Source == nil {
		c.Source = &GitHubSource{
			Owner:           c.Owner,
			Repo:            c.Repo,
			Token:           c.Token,
			HTTPClient:      c.HTTPClient,
			AllowPrerelease: c.AllowPrerelease,
		}
	}
	if c.Verifier == nil {
		c.Verifier = ChecksumVerifier{}
	}
	return c, nil
}
