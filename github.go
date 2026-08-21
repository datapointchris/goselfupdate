package goselfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultGitHubAPI is the API root used when GitHubSource.APIBase is empty.
// Point APIBase at a GitHub Enterprise installation's /api/v3 root to update
// from one.
const DefaultGitHubAPI = "https://api.github.com"

// maxResponseSize bounds every response read into memory. Release archives are
// tens of megabytes; this is large enough for any plausible one and small
// enough that a hostile or misconfigured endpoint cannot exhaust memory.
const maxResponseSize = 512 << 20

// GitHubSource reads releases from GitHub's REST API.
type GitHubSource struct {
	// Owner and Repo identify the repository.
	Owner string
	Repo  string

	// Token authenticates requests. Leave it empty and the source resolves one
	// itself: $GITHUB_TOKEN, then $GH_TOKEN, then TokenFunc, then
	// $GITHUB_TOKEN_COMMAND — which defaults to `gh auth token`.
	//
	// Without any credential GitHub permits 60 requests per hour per IP address,
	// shared with every other anonymous caller behind the same egress, and
	// denies private repositories.
	Token string

	// TokenFunc is a source of the caller's own, tried after the environment and
	// before the command. Returning "" falls through.
	//
	// Reaching for gh no longer needs one — that is the default. This is for a
	// credential neither the environment nor a command can produce.
	//
	// Called only when a request is about to be made, because a credential can
	// be expensive to obtain and [autoupdate]'s gate declines most invocations
	// without touching the network.
	TokenFunc func() string

	// resolved caches the credential for this source's lifetime. A check that
	// also fetches a changelog makes several requests, and the command behind
	// this can be a vault unlock.
	resolveOnce sync.Once
	resolved    string

	// HTTPClient performs requests. Defaults to http.DefaultClient.
	HTTPClient *http.Client

	// APIBase overrides [DefaultGitHubAPI].
	APIBase string

	// AllowPrerelease returns the newest release even if it is marked as a
	// prerelease. GitHub's "latest release" endpoint excludes them, so this
	// selects a different endpoint.
	AllowPrerelease bool

	// TagPrefix selects one release stream in a repository that publishes
	// several, as in "cli/" for tags of the form cli/v1.2.3. Empty means the
	// repository publishes a single stream tagged v1.2.3, which is the common
	// case and the default.
	//
	// A prefix is not cosmetic: Go requires a module in a subdirectory to be
	// tagged with that subdirectory, and GitHub's "latest release" endpoint is
	// repository-wide, so it will happily return another component's release.
	// Setting this switches to the release list and filters it, which is the
	// only way to ask for the newest release of one stream.
	//
	// Reported tags have the prefix removed, so [Release.Tag] is a version as
	// documented and callers never see the repository's tag layout.
	TagPrefix string
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		// The API asset endpoint, not browser_download_url. The latter is a
		// github.com link that ignores an Authorization header, so a private
		// repository answers it with 404 however good the token is. The API
		// URL authenticates and redirects to the same bytes, and works
		// unauthenticated on a public repository, so it is right for both.
		URL  string `json:"url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (r githubRelease) toRelease(tagPrefix string) Release {
	assets := make([]Asset, 0, len(r.Assets))
	for _, a := range r.Assets {
		assets = append(assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return Release{
		Tag:        strings.TrimPrefix(r.TagName, tagPrefix),
		Assets:     assets,
		Prerelease: r.Prerelease,
	}
}

// LatestRelease implements [Source].
func (s *GitHubSource) LatestRelease(ctx context.Context) (Release, error) {
	if s.TagPrefix != "" {
		return s.highestPrefixedRelease(ctx)
	}
	if s.AllowPrerelease {
		return s.newestRelease(ctx)
	}

	var release githubRelease
	if err := s.getJSON(ctx, s.url("releases/latest"), &release); err != nil {
		return Release{}, err
	}
	if release.TagName == "" {
		return Release{}, fmt.Errorf("%w for %s/%s", ErrNoRelease, s.Owner, s.Repo)
	}
	return release.toRelease(""), nil
}

// newestRelease takes the first non-draft entry from the release list, which
// GitHub returns newest-first by creation date.
func (s *GitHubSource) newestRelease(ctx context.Context) (Release, error) {
	var releases []githubRelease
	if err := s.getJSON(ctx, s.url("releases?per_page=20"), &releases); err != nil {
		return Release{}, err
	}
	for _, release := range releases {
		if !release.Draft && release.TagName != "" {
			return release.toRelease(""), nil
		}
	}
	return Release{}, fmt.Errorf("%w for %s/%s", ErrNoRelease, s.Owner, s.Repo)
}

// highestPrefixedRelease returns the highest-versioned release carrying
// TagPrefix.
//
// It selects by version rather than by position, unlike [newestRelease]. Within
// one stream those can disagree — a patch to an older line (cli/v1.4.1)
// published after a newer minor (cli/v1.5.0) sits earlier in a list GitHub
// orders by creation date — and offering that as an "update" would move the
// caller backwards.
//
// The page is the API maximum because the list interleaves every stream in the
// repository: the component being asked about may be far down a page otherwise
// dominated by another one.
func (s *GitHubSource) highestPrefixedRelease(ctx context.Context) (Release, error) {
	var releases []githubRelease
	if err := s.getJSON(ctx, s.url("releases?per_page=100"), &releases); err != nil {
		return Release{}, err
	}

	var best Release
	for _, release := range releases {
		if release.Draft || release.TagName == "" {
			continue
		}
		if release.Prerelease && !s.AllowPrerelease {
			continue
		}
		if !strings.HasPrefix(release.TagName, s.TagPrefix) {
			continue
		}
		candidate := release.toRelease(s.TagPrefix)
		if !isValidVersion(canonical(candidate.Tag)) {
			continue
		}
		if best.Tag == "" || compareVersion(canonical(candidate.Tag), canonical(best.Tag)) > 0 {
			best = candidate
		}
	}

	if best.Tag == "" {
		return Release{}, fmt.Errorf("%w for %s/%s with tag prefix %q",
			ErrNoRelease, s.Owner, s.Repo, s.TagPrefix)
	}
	return best, nil
}

// Download implements [Source].
// The asset endpoint serves metadata by default and the file itself only when
// asked for bytes, so this Accept is what makes a download a download.
func (s *GitHubSource) Download(ctx context.Context, asset Asset) ([]byte, error) {
	return s.fetch(ctx, asset.URL, "application/octet-stream")
}

// Changelog implements [Changeloger], returning the subject line of every
// commit between two tags.
func (s *GitHubSource) Changelog(ctx context.Context, fromTag, toTag string) ([]string, error) {
	var payload struct {
		Commits []struct {
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		} `json:"commits"`
	}
	// The versions handed in come from [Release.Tag], which has TagPrefix
	// stripped — but compare resolves git refs, and the refs that exist in the
	// repository are the prefixed ones.
	from := s.TagPrefix + fromTag
	to := s.TagPrefix + toTag
	if err := s.getJSON(ctx, s.url("compare/"+from+"..."+to), &payload); err != nil {
		return nil, err
	}

	subjects := make([]string, 0, len(payload.Commits))
	for _, entry := range payload.Commits {
		subject, _, _ := strings.Cut(entry.Commit.Message, "\n")
		if subject != "" {
			subjects = append(subjects, subject)
		}
	}
	return subjects, nil
}

func (s *GitHubSource) url(path string) string {
	base := s.APIBase
	if base == "" {
		base = DefaultGitHubAPI
	}
	return fmt.Sprintf("%s/repos/%s/%s/%s", strings.TrimSuffix(base, "/"), s.Owner, s.Repo, path)
}

func (s *GitHubSource) getJSON(ctx context.Context, url string, into any) error {
	body, err := s.get(ctx, url)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func (s *GitHubSource) get(ctx context.Context, url string) ([]byte, error) {
	return s.fetch(ctx, url, "application/vnd.github+json")
}

// credential resolves the token once, on first use.
//
// Four sources, first non-empty wins. It lives here rather than on Config
// because a credential is the host's business: a Source for another forge
// brings its own variable and its own command, and nothing above the Source
// interface learns either name.
func (s *GitHubSource) credential() string {
	s.resolveOnce.Do(func() {
		for _, candidate := range []func() string{
			func() string { return s.Token },
			tokenFromEnv,
			func() string {
				if s.TokenFunc == nil {
					return ""
				}
				return s.TokenFunc()
			},
			tokenFromCommand,
		} {
			if value := candidate(); value != "" {
				s.resolved = value
				return
			}
		}
	})
	return s.resolved
}

// fetch performs an authenticated GET. accept distinguishes an API read from an
// asset download; the redirect to the CDN that a download ends in drops the
// Authorization header on its own, since net/http does not carry credentials
// across hosts.
func (s *GitHubSource) fetch(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	token := s.credential()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, url, token != "")
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
}

// statusError turns a failed response into a message that names the likely
// cause. A bare "403 Forbidden" from GitHub is almost always rate limiting,
// which is fixed by a token rather than by retrying.
func statusError(resp *http.Response, url string, authenticated bool) error {
	switch {
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return rateLimitError(resp, authenticated)
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("github rejected the token for %s", url)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s returned 404 (private repositories need a token)", ErrNoRelease, url)
	default:
		return fmt.Errorf("github: %s for %s", resp.Status, url)
	}
}

// rateLimitError names which ceiling was hit, because the two are different
// problems with different fixes.
//
// One sentence for both misadvised whichever case it was not written for, and
// telling someone to set a token when the request already carried one is the
// worse half: it reads as advice to configure something already configured. The
// authenticated case should be rare, so it says when the ceiling lifts rather
// than what to change — there is nothing to change.
func rateLimitError(resp *http.Response, authenticated bool) error {
	resets := resetClock(resp.Header.Get("X-RateLimit-Reset"))
	if authenticated {
		return fmt.Errorf("github's authenticated rate limit (5,000/hour) is exhausted%s", resets)
	}
	return fmt.Errorf(
		"github's anonymous rate limit (60/hour, shared by every host on this IP) is exhausted%s; "+
			"run `gh auth login`, or set GITHUB_TOKEN, for the 5,000/hour authenticated limit", resets)
}

// resetClock renders the reset header as a wall-clock time, or nothing when it
// is absent or unusable.
//
// A time rather than a duration, because the message is read out of a state
// file long after the request that produced it and a duration would be counted
// from the wrong instant. The raw header is an epoch integer, which is what the
// message used to print.
func resetClock(header string) string {
	seconds, err := strconv.ParseInt(header, 10, 64)
	if err != nil {
		return ""
	}
	return "; resets at " + time.Unix(seconds, 0).UTC().Format("15:04") + " UTC"
}
