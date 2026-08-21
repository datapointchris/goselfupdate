package goselfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// newStubRelease builds a release carrying a platform archive and a matching
// checksums file, which is the shape goreleaser publishes.
func newStubRelease(t *testing.T, tag string) *stubSource {
	t.Helper()

	archive := makeTarGz(t, map[string]string{"tool": binaryContent})
	archiveName := platformName("tool_%s_%s.tar.gz")
	checksumsName := "tool_checksums.txt"

	return &stubSource{
		release: Release{
			Tag:    tag,
			Assets: []Asset{{Name: archiveName}, {Name: checksumsName}},
		},
		bodies: map[string][]byte{
			archiveName:   archive,
			checksumsName: []byte(fmt.Sprintf("%s  %s\n", digestOf(archive), archiveName)),
		},
	}
}

func stubConfig(source Source, version string) Config {
	return Config{Binary: "tool", Version: version, Source: source}
}

func stageBinary(t *testing.T) string {
	t.Helper()

	target := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestUpdateToInstallsNewerRelease(t *testing.T) {
	source := newStubRelease(t, "v1.2.0")
	target := stageBinary(t)

	result, err := UpdateTo(context.Background(), stubConfig(source, "v1.1.0"), target)
	if err != nil {
		t.Fatalf("UpdateTo: %v", err)
	}
	if !result.Applied {
		t.Error("Applied should be true")
	}
	if result.From != "v1.1.0" || result.To != "v1.2.0" {
		t.Errorf("got %s → %s", result.From, result.To)
	}

	installed, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != binaryContent {
		t.Errorf("binary not replaced: %q", installed)
	}
}

func TestUpdateToIsANoopWhenCurrent(t *testing.T) {
	for _, running := range []string{"v1.2.0", "v1.3.0"} {
		source := newStubRelease(t, "v1.2.0")
		target := stageBinary(t)

		result, err := UpdateTo(context.Background(), stubConfig(source, running), target)
		if err != nil {
			t.Fatalf("running %s: %v", running, err)
		}
		if result.Applied || result.UpdateAvailable() {
			t.Errorf("running %s: unexpectedly updated", running)
		}

		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "old binary" {
			t.Errorf("running %s: binary was replaced", running)
		}
	}
}

func TestUpdateToRejectsDevBuilds(t *testing.T) {
	for _, running := range []string{"", "dev", "(devel)", "unknown"} {
		source := newStubRelease(t, "v1.2.0")
		_, err := UpdateTo(context.Background(), stubConfig(source, running), stageBinary(t))
		if !errors.Is(err, ErrDevBuild) {
			t.Errorf("version %q: got %v, want ErrDevBuild", running, err)
		}
	}
}

// A bad checksum must stop the update before the existing binary is touched.
func TestUpdateToLeavesBinaryIntactOnChecksumMismatch(t *testing.T) {
	source := newStubRelease(t, "v1.2.0")
	source.bodies["tool_checksums.txt"] = []byte("0000  " + platformName("tool_%s_%s.tar.gz") + "\n")

	target := stageBinary(t)

	_, err := UpdateTo(context.Background(), stubConfig(source, "v1.1.0"), target)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old binary" {
		t.Error("binary was replaced despite a failed checksum")
	}
}

func TestUpdateToPropagatesSourceFailure(t *testing.T) {
	sentinel := errors.New("network down")
	source := &stubSource{err: sentinel}

	_, err := UpdateTo(context.Background(), stubConfig(source, "v1.0.0"), stageBinary(t))
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want the source error", err)
	}
}

func TestUpdateToRejectsNonSemverTag(t *testing.T) {
	source := newStubRelease(t, "not-a-version")

	_, err := UpdateTo(context.Background(), stubConfig(source, "v1.0.0"), stageBinary(t))
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("got %v, want ErrNoRelease", err)
	}
}

func TestUpdateToHonoursCustomVerifier(t *testing.T) {
	source := newStubRelease(t, "v1.2.0")
	// Remove the checksums file so the default verifier would fail.
	source.release.Assets = source.release.Assets[:1]

	cfg := stubConfig(source, "v1.1.0")
	cfg.Verifier = NoVerification{}

	result, err := UpdateTo(context.Background(), cfg, stageBinary(t))
	if err != nil {
		t.Fatalf("UpdateTo: %v", err)
	}
	if !result.Applied {
		t.Error("expected the update to be applied")
	}
}

func TestCheckDoesNotWrite(t *testing.T) {
	source := newStubRelease(t, "v2.0.0")

	result, err := Check(context.Background(), stubConfig(source, "v1.0.0"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Applied {
		t.Error("Check reported an applied update")
	}
	if !result.UpdateAvailable() || result.To != "v2.0.0" {
		t.Errorf("got %+v", result)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := map[string]Config{
		"no binary":        {Owner: "o", Repo: "r", Version: "v1.0.0"},
		"no owner or repo": {Binary: "tool", Version: "v1.0.0"},
	}
	for name, cfg := range cases {
		if _, err := Check(context.Background(), cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: got %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestConfigDefaultsToGitHubSource(t *testing.T) {
	resolved, err := Config{Owner: "o", Repo: "r", Binary: "tool", Version: "v1.0.0"}.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resolved.Source.(*GitHubSource); !ok {
		t.Errorf("Source is %T, want *GitHubSource", resolved.Source)
	}
	if _, ok := resolved.Verifier.(ChecksumVerifier); !ok {
		t.Errorf("Verifier is %T, want ChecksumVerifier", resolved.Verifier)
	}
	if resolved.HTTPClient == nil {
		t.Error("HTTPClient was not defaulted")
	}
}

// TagPrefix has to reach the default source, which is the only thing that reads
// it — a Config field that silently went nowhere would fail as "not a semantic
// version" at the first Check.
func TestConfigCarriesTagPrefixToGitHubSource(t *testing.T) {
	resolved, err := Config{
		Owner: "o", Repo: "r", Binary: "tool", Version: "v1.0.0", TagPrefix: "cli/",
	}.resolve()
	if err != nil {
		t.Fatal(err)
	}
	source, ok := resolved.Source.(*GitHubSource)
	if !ok {
		t.Fatalf("Source is %T, want *GitHubSource", resolved.Source)
	}
	if source.TagPrefix != "cli/" {
		t.Errorf("TagPrefix = %q, want it carried to the source", source.TagPrefix)
	}
}

// TokenFunc is the fallback of last resort, so an explicit Token and both
// environment variables have to win over it — a caller that already has a
// credential must never pay for the expensive one.
func TestTokenFuncIsOnlyConsultedWhenNothingElseSuppliesTheToken(t *testing.T) {
	cases := []struct {
		name        string
		token       string
		environment map[string]string
		want        string
		wantCalls   int
	}{
		{name: "nothing else", want: "from-func", wantCalls: 1},
		{name: "explicit token", token: "explicit", want: "explicit"},
		{name: "GH_TOKEN", environment: map[string]string{"GH_TOKEN": "from-gh"}, want: "from-gh"},
		{name: "GITHUB_TOKEN", environment: map[string]string{"GITHUB_TOKEN": "from-env"}, want: "from-env"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("GH_TOKEN", "")
			for name, value := range testCase.environment {
				t.Setenv(name, value)
			}

			calls := 0
			resolved, err := Config{
				Owner: "o", Repo: "r", Binary: "tool", Version: "v1.0.0",
				Token: testCase.token,
				TokenFunc: func() string {
					calls++
					return "from-func"
				},
			}.resolve()
			if err != nil {
				t.Fatal(err)
			}

			source, ok := resolved.Source.(*GitHubSource)
			if !ok {
				t.Fatalf("Source is %T, want *GitHubSource", resolved.Source)
			}
			// resolve() no longer fills Config.Token. The credential is the
			// host's business, so the source resolves it and Config only hands
			// down what the caller set.
			if resolved.Token != testCase.token {
				t.Errorf("Config.Token = %q, want it left as passed (%q)", resolved.Token, testCase.token)
			}
			if calls != 0 {
				t.Errorf("TokenFunc called %d times during resolve, want 0", calls)
			}

			t.Setenv(TokenCommandEnv, "")
			if got := source.credential(); got != testCase.want {
				t.Errorf("credential() = %q, want %q", got, testCase.want)
			}
			if calls != testCase.wantCalls {
				t.Errorf("TokenFunc called %d times, want %d", calls, testCase.wantCalls)
			}
		})
	}
}

// One lever that both redirects and disables, which is what a switch has to do
// to be worth having.
func TestTheTokenCommandRedirectsAndDisables(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{name: "redirected", command: "printf redirected", want: "redirected"},
		{name: "disabled", command: "", want: ""},
		{name: "command exits non-zero", command: "false", want: ""},
		{name: "command not installed", command: "no-such-binary-anywhere", want: ""},
		{name: "quoted argument", command: `printf "one two"`, want: "one two"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("GH_TOKEN", "")
			t.Setenv(TokenCommandEnv, testCase.command)

			source := &GitHubSource{Owner: "o", Repo: "r"}
			if got := source.credential(); got != testCase.want {
				t.Errorf("credential() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// A check that also fetches a changelog makes several requests, and the command
// behind this can be a vault unlock with a touch prompt.
func TestTheCredentialResolvesOncePerSource(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv(TokenCommandEnv, "")

	calls := 0
	source := &GitHubSource{Owner: "o", Repo: "r", TokenFunc: func() string {
		calls++
		return "from-func"
	}}

	source.credential()
	source.credential()

	if calls != 1 {
		t.Errorf("TokenFunc called %d times, want 1", calls)
	}
}

// The whole point of TokenFunc is that it is not called until a request is
// about to be made. Building a Config must therefore be free, because the
// autoupdate gate runs against one on every invocation and declines most of
// them without ever reaching the network.
func TestBuildingAConfigDoesNotCallTokenFunc(t *testing.T) {
	called := false
	config := Config{
		Owner: "o", Repo: "r", Binary: "tool", Version: "v1.0.0",
		TokenFunc: func() string {
			called = true
			return ""
		},
	}

	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("TokenFunc ran before a request was made")
	}
}

func TestChangelogReturnsNilForSourceWithout(t *testing.T) {
	// A Source that does not implement Changeloger produces no changelog
	// rather than an error.
	cfg := Config{Binary: "tool", Version: "v1.0.0", Source: sourceWithoutChangelog{}}

	subjects, err := Changelog(context.Background(), cfg, "v1.0.0", "v1.1.0")
	if err != nil {
		t.Fatalf("Changelog: %v", err)
	}
	if subjects != nil {
		t.Errorf("got %v, want nil", subjects)
	}
}

type sourceWithoutChangelog struct{}

func (sourceWithoutChangelog) LatestRelease(context.Context) (Release, error) {
	return Release{Tag: "v1.0.0"}, nil
}

func (sourceWithoutChangelog) Download(context.Context, Asset) ([]byte, error) {
	return nil, nil
}
