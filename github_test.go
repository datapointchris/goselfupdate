package goselfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newGitHubServer(t *testing.T, handler http.HandlerFunc) *GitHubSource {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &GitHubSource{
		Owner:      "datapointchris",
		Repo:       "tool",
		APIBase:    server.URL,
		HTTPClient: server.Client(),
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubSourceLatestRelease(t *testing.T) {
	source := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/datapointchris/tool/releases/latest" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"tag_name": "v1.2.0",
			"assets": []map[string]any{
				{"name": "tool_1.2.0_linux_amd64.tar.gz", "browser_download_url": "http://example/a", "size": 42},
			},
		})
	})

	release, err := source.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if release.Tag != "v1.2.0" {
		t.Errorf("got tag %s", release.Tag)
	}
	if len(release.Assets) != 1 || release.Assets[0].Size != 42 {
		t.Errorf("got assets %+v", release.Assets)
	}
}

func TestGitHubSourceSendsAuthorizationAndVersionHeaders(t *testing.T) {
	var gotAuth, gotVersion string
	source := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		writeJSON(t, w, map[string]any{"tag_name": "v1.0.0"})
	})
	source.Token = "secret-token"

	if _, err := source.LatestRelease(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotVersion != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q", gotVersion)
	}
}

func TestGitHubSourceOmitsAuthorizationWithoutToken(t *testing.T) {
	var hadAuth bool
	source := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		writeJSON(t, w, map[string]any{"tag_name": "v1.0.0"})
	})

	if _, err := source.LatestRelease(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hadAuth {
		t.Error("sent an Authorization header without a token")
	}
}

// Without a token GitHub allows 60 requests an hour, so this is the failure a
// real user hits first. The message has to name the cause.
func TestGitHubSourceExplainsRateLimiting(t *testing.T) {
	source := newGitHubServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusForbidden)
	})

	_, err := source.LatestRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("got %v, want a rate limit explanation", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error does not suggest a token: %v", err)
	}
}

func TestGitHubSourceExplains404(t *testing.T) {
	source := newGitHubServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := source.LatestRelease(context.Background())
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("got %v, want ErrNoRelease", err)
	}
	if !strings.Contains(err.Error(), "private") {
		t.Errorf("error does not mention private repositories: %v", err)
	}
}

func TestGitHubSourceReportsNoRelease(t *testing.T) {
	source := newGitHubServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{})
	})

	if _, err := source.LatestRelease(context.Background()); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("got %v, want ErrNoRelease", err)
	}
}

// GitHub's "latest release" endpoint excludes prereleases, so AllowPrerelease
// has to select the list endpoint instead and skip drafts.
func TestGitHubSourceAllowPrereleaseUsesListEndpoint(t *testing.T) {
	var requested string
	source := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		writeJSON(t, w, []map[string]any{
			{"tag_name": "v2.0.0-rc.1", "draft": true, "prerelease": true},
			{"tag_name": "v2.0.0-rc.2", "draft": false, "prerelease": true},
			{"tag_name": "v1.0.0", "draft": false, "prerelease": false},
		})
	})
	source.AllowPrerelease = true

	release, err := source.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if !strings.HasSuffix(requested, "/releases") {
		t.Errorf("requested %s, want the releases list", requested)
	}
	if release.Tag != "v2.0.0-rc.2" {
		t.Errorf("got %s, want the newest non-draft release", release.Tag)
	}
	if !release.Prerelease {
		t.Error("Prerelease flag was not carried through")
	}
}

func TestGitHubSourceChangelogReturnsSubjectsOnly(t *testing.T) {
	source := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "compare/v1.0.0...v1.1.0") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"commits": []map[string]any{
				{"commit": map[string]any{"message": "feat: add a thing\n\nlong body text"}},
				{"commit": map[string]any{"message": "fix: repair another"}},
				{"commit": map[string]any{"message": ""}},
			},
		})
	})

	subjects, err := source.Changelog(context.Background(), "v1.0.0", "v1.1.0")
	if err != nil {
		t.Fatalf("Changelog: %v", err)
	}
	want := []string{"feat: add a thing", "fix: repair another"}
	if len(subjects) != len(want) {
		t.Fatalf("got %v, want %v", subjects, want)
	}
	for i := range want {
		if subjects[i] != want[i] {
			t.Errorf("subject %d = %q, want %q", i, subjects[i], want[i])
		}
	}
}

func TestGitHubSourceDefaultAPIBase(t *testing.T) {
	source := &GitHubSource{Owner: "o", Repo: "r"}
	if got := source.url("releases/latest"); got != DefaultGitHubAPI+"/repos/o/r/releases/latest" {
		t.Errorf("got %s", got)
	}
}

// A GitHub Enterprise root is given with a path and possibly a trailing slash.
func TestGitHubSourceHonoursCustomAPIBase(t *testing.T) {
	source := &GitHubSource{Owner: "o", Repo: "r", APIBase: "https://git.example.com/api/v3/"}
	want := "https://git.example.com/api/v3/repos/o/r/releases/latest"
	if got := source.url("releases/latest"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestTokenFromEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if got := tokenFromEnv(); got != "" {
		t.Errorf("got %q, want empty", got)
	}

	t.Setenv("GH_TOKEN", "from-gh")
	if got := tokenFromEnv(); got != "from-gh" {
		t.Errorf("got %q, want from-gh", got)
	}

	// GITHUB_TOKEN takes precedence.
	t.Setenv("GITHUB_TOKEN", "from-github")
	if got := tokenFromEnv(); got != "from-github" {
		t.Errorf("got %q, want from-github", got)
	}
}
