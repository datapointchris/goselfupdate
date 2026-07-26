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
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (r githubRelease) toRelease() Release {
	assets := make([]Asset, 0, len(r.Assets))
	for _, a := range r.Assets {
		assets = append(assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return Release{Tag: r.TagName, Assets: assets, Prerelease: r.Prerelease}
}

// LatestRelease implements [Source].
func (s *GitHubSource) LatestRelease(ctx context.Context) (Release, error) {
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
	return release.toRelease(), nil
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
			return release.toRelease(), nil
		}
	}
	return Release{}, fmt.Errorf("%w for %s/%s", ErrNoRelease, s.Owner, s.Repo)
}

// Download implements [Source].
func (s *GitHubSource) Download(ctx context.Context, asset Asset) ([]byte, error) {
	return s.get(ctx, asset.URL)
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
	if err := s.getJSON(ctx, s.url("compare/"+fromTag+"..."+toTag), &payload); err != nil {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
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
