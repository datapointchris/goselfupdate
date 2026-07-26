package goselfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

	// Token authenticates requests. Without one GitHub permits 60 requests per
	// hour per IP address and denies private repositories.
	Token string

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
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
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
		return nil, statusError(resp, url)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
}

// statusError turns a failed response into a message that names the likely
// cause. A bare "403 Forbidden" from GitHub is almost always rate limiting,
// which is fixed by a token rather than by retrying.
func statusError(resp *http.Response, url string) error {
	switch {
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return fmt.Errorf("github rate limit exceeded (resets at %s); set a token to raise the limit",
			resp.Header.Get("X-RateLimit-Reset"))
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("github rejected the token for %s", url)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s returned 404 (private repositories need a token)", ErrNoRelease, url)
	default:
		return fmt.Errorf("github: %s for %s", resp.Status, url)
	}
}

func tokenFromEnv() string {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
