package autoupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/datapointchris/goselfupdate"
)

// stubSource serves a fixed release and counts how often it was asked.
//
// The count is the important half: most of these tests assert that nothing
// happened, and a skip that still reached the network is a skip that failed.
type stubSource struct {
	tag   string
	err   error
	calls atomic.Int64
}

func (s *stubSource) LatestRelease(context.Context) (goselfupdate.Release, error) {
	s.calls.Add(1)
	if s.err != nil {
		return goselfupdate.Release{}, s.err
	}
	return goselfupdate.Release{Tag: s.tag}, nil
}

func (s *stubSource) Download(context.Context, goselfupdate.Asset) ([]byte, error) {
	return nil, nil
}

// harness builds a Config wired to a temporary state directory and an empty
// environment, so a developer's own CI=1 cannot change a result.
type harness struct {
	config Config
	source *stubSource
	out    *strings.Builder
	dir    string
	env    map[string]string
	now    time.Time
}

func newHarness(t *testing.T, latest string) *harness {
	t.Helper()

	interactive := true
	h := &harness{
		source: &stubSource{tag: latest},
		out:    &strings.Builder{},
		dir:    t.TempDir(),
		env:    map[string]string{},
		now:    time.Unix(1_785_000_000, 0),
	}
	h.config = Config{
		Update: goselfupdate.Config{
			Owner:   "datapointchris",
			Repo:    "demo",
			Binary:  "demo",
			Version: "v1.0.0",
			Source:  h.source,
		},
		Out:         h.out,
		StateDir:    h.dir,
		Interactive: &interactive,
		Clock:       func() time.Time { return h.now },
		Environ: func(name string) (string, bool) {
			value, ok := h.env[name]
			return value, ok
		},
	}
	return h
}

func (h *harness) run(ctx context.Context) Outcome {
	return Start(ctx, h.config).Finish()
}

func (h *harness) state(t *testing.T) State {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, "demo", StateFilename))
	if err != nil {
		t.Fatalf("state file: %v", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("state file is not valid JSON: %v", err)
	}
	return state
}

func (h *harness) stateExists() bool {
	_, err := os.Stat(filepath.Join(h.dir, "demo", StateFilename))
	return err == nil
}

func TestNotifiesWhenBehind(t *testing.T) {
	h := newHarness(t, "v2.0.0")

	outcome := h.run(context.Background())

	if !outcome.Checked || !outcome.UpdateAvailable() {
		t.Fatalf("expected an available update, got %+v", outcome)
	}
	if got := h.out.String(); !strings.Contains(got, "demo v2.0.0 available (running v1.0.0)") {
		t.Errorf("notice missing or wrong: %q", got)
	}
	if !strings.Contains(h.out.String(), "run `demo update`") {
		t.Errorf("notice does not say what to run: %q", h.out.String())
	}
}

func TestSaysNothingWhenCurrent(t *testing.T) {
	h := newHarness(t, "v1.0.0")

	outcome := h.run(context.Background())

	if outcome.UpdateAvailable() {
		t.Errorf("reported an update against an identical version: %+v", outcome)
	}
	if h.out.Len() != 0 {
		t.Errorf("printed something when current: %q", h.out.String())
	}
}

func TestSaysNothingWhenAheadOfTheRelease(t *testing.T) {
	h := newHarness(t, "v0.9.0")

	if h.run(context.Background()).UpdateAvailable() {
		t.Error("running ahead of the published release is not a downgrade prompt")
	}
}

func TestDoesNotCheckTwiceInsideTheInterval(t *testing.T) {
	h := newHarness(t, "v2.0.0")

	h.run(context.Background())
	second := h.run(context.Background())

	if second.Skip != SkipInterval {
		t.Errorf("second run skip = %q, want %q", second.Skip, SkipInterval)
	}
	if got := h.source.calls.Load(); got != 1 {
		t.Errorf("source consulted %d times, want 1", got)
	}
}

func TestChecksAgainOnceTheIntervalHasElapsed(t *testing.T) {
	h := newHarness(t, "v2.0.0")

	h.run(context.Background())
	h.now = h.now.Add(25 * time.Hour)
	h.run(context.Background())

	if got := h.source.calls.Load(); got != 2 {
		t.Errorf("source consulted %d times, want 2", got)
	}
}

// The ordering that gh gets wrong: it stamps only on success, so a
// rate-limited or offline user re-hits the API on every single invocation until
// the window resets. An interval exists to bound the request rate, and only
// stamping first actually does that.
func TestTheTimestampIsWrittenBeforeTheCheckNotAfter(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	h.source.err = goselfupdate.ErrNoRelease

	outcome := h.run(context.Background())

	if outcome.Skip != SkipFailed {
		t.Fatalf("skip = %q, want %q", outcome.Skip, SkipFailed)
	}
	state := h.state(t)
	if state.CheckedAtEpoch == 0 {
		t.Fatal("a failed check must still consume its interval")
	}
	if state.LastError == "" {
		t.Error("the failure was not recorded")
	}

	if second := h.run(context.Background()); second.Skip != SkipInterval {
		t.Errorf("the run after a failure skip = %q, want %q", second.Skip, SkipInterval)
	}
	if got := h.source.calls.Load(); got != 1 {
		t.Errorf("source consulted %d times after a failure, want 1", got)
	}
}

func TestAFailurePrintsNothing(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	h.source.err = goselfupdate.ErrNoRelease

	h.run(context.Background())

	if h.out.Len() != 0 {
		t.Errorf("the notify path printed an error: %q", h.out.String())
	}
}

func TestALaterSuccessClearsTheRecordedError(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	h.source.err = goselfupdate.ErrNoRelease
	h.run(context.Background())

	h.source.err = nil
	h.now = h.now.Add(25 * time.Hour)
	h.run(context.Background())

	if got := h.state(t).LastError; got != "" {
		t.Errorf("last_error = %q, want empty", got)
	}
}

// Presence-only, any value, so NO_AUTO_UPDATE=0 cannot mean "on".
func TestTheKillSwitchIsPresenceOnly(t *testing.T) {
	for _, variable := range []string{"NO_AUTO_UPDATE", "DEMO_NO_AUTO_UPDATE"} {
		for _, value := range []string{"", "0", "false", "1"} {
			t.Run(variable+"="+value, func(t *testing.T) {
				h := newHarness(t, "v2.0.0")
				h.env[variable] = value

				if got := h.run(context.Background()).Skip; got != SkipDisabled {
					t.Errorf("skip = %q, want %q", got, SkipDisabled)
				}
				if h.source.calls.Load() != 0 {
					t.Error("the source was consulted despite the kill switch")
				}
				if h.stateExists() {
					t.Error("a skipped run wrote a state file")
				}
			})
		}
	}
}

func TestCIIsNeverNotified(t *testing.T) {
	for _, variable := range ciVariables {
		t.Run(variable, func(t *testing.T) {
			h := newHarness(t, "v2.0.0")
			h.env[variable] = "1"

			if got := h.run(context.Background()).Skip; got != SkipCI {
				t.Errorf("skip = %q, want %q", got, SkipCI)
			}
			if h.source.calls.Load() != 0 {
				t.Error("the source was consulted in CI")
			}
		})
	}
}

func TestANonTerminalIsNeverNotified(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	notATerminal := false
	h.config.Interactive = &notATerminal

	if got := h.run(context.Background()).Skip; got != SkipNotATTY {
		t.Errorf("skip = %q, want %q", got, SkipNotATTY)
	}
	if h.source.calls.Load() != 0 {
		t.Error("the source was consulted while not on a terminal")
	}
}

// A Go build pseudo-version is valid semver, so the gate cannot merely parse
// the version -- it has to ask whether it is a plain release.
func TestADevBuildIsNeverNotified(t *testing.T) {
	for _, version := range []string{
		"dev",
		"",
		"v1.6.1-0.20260724161156-2c04703+dirty",
		"v1.0.0-rc.1",
		"v1.0.0+dirty",
	} {
		t.Run(version, func(t *testing.T) {
			h := newHarness(t, "v2.0.0")
			h.config.Update.Version = version

			if got := h.run(context.Background()).Skip; got != SkipDevBuild {
				t.Errorf("skip = %q, want %q", got, SkipDevBuild)
			}
			if h.source.calls.Load() != 0 {
				t.Error("a dev build reached the network")
			}
			if h.stateExists() {
				t.Error("a dev build wrote a state file")
			}
		})
	}
}

func TestSuppressSkipsWithoutTouchingAnything(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	h.config.Suppress = true

	if got := h.run(context.Background()).Skip; got != SkipSuppressed {
		t.Errorf("skip = %q, want %q", got, SkipSuppressed)
	}
	if h.source.calls.Load() != 0 {
		t.Error("a suppressed command reached the network")
	}
}

func TestEnabledReportsTheReasonWithoutTouchingTheNetwork(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	h.env["CI"] = "1"

	allowed, skip := Enabled(h.config)

	if allowed || skip != SkipCI {
		t.Errorf("Enabled = (%v, %q), want (false, %q)", allowed, skip, SkipCI)
	}
	if h.source.calls.Load() != 0 {
		t.Error("Enabled consulted the source")
	}
}

func TestStateCarriesBothTimestampForms(t *testing.T) {
	h := newHarness(t, "v2.0.0")

	h.run(context.Background())

	state := h.state(t)
	if state.Schema != Schema {
		t.Errorf("schema = %d, want %d", state.Schema, Schema)
	}
	if state.Tool != "demo" {
		t.Errorf("tool = %q, want demo", state.Tool)
	}
	if !strings.HasSuffix(state.CheckedAt, "Z") {
		t.Errorf("checked_at = %q, want a UTC timestamp", state.CheckedAt)
	}
	// The epoch field is what lets the bash sibling do the same arithmetic with
	// jq and date alone.
	if state.CheckedAtEpoch != h.now.Unix() {
		t.Errorf("checked_at_epoch = %d, want %d", state.CheckedAtEpoch, h.now.Unix())
	}
	if state.CurrentVersion != "v1.0.0" || state.LatestVersion != "v2.0.0" {
		t.Errorf("versions = %q -> %q", state.CurrentVersion, state.LatestVersion)
	}
}

func TestStateSurvivesACorruptFile(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	path := filepath.Join(h.dir, "demo")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, StateFilename), []byte("not json {{{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if outcome := h.run(context.Background()); !outcome.Checked {
		t.Errorf("a corrupt state file blocked the check: %+v", outcome)
	}
}

func TestFinishIsIdempotent(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	session := Start(context.Background(), h.config)

	first := session.Finish()
	second := session.Finish()

	if first != second {
		t.Errorf("Finish returned %+v then %+v", first, second)
	}
	if count := strings.Count(h.out.String(), "available"); count != 1 {
		t.Errorf("printed the notice %d times, want 1", count)
	}
}

func TestParseInterval(t *testing.T) {
	valid := map[string]time.Duration{
		"30s":  30 * time.Second,
		"30m":  30 * time.Minute,
		"24h":  24 * time.Hour,
		"7d":   7 * 24 * time.Hour,
		"90":   90 * time.Second,
		"1.5h": 90 * time.Minute,
	}
	for raw, want := range valid {
		got, ok := parseInterval(raw)
		if !ok || got != want {
			t.Errorf("parseInterval(%q) = (%v, %v), want (%v, true)", raw, got, ok, want)
		}
	}

	for _, raw := range []string{"", "soon", "-1h", "h", "abc"} {
		if _, ok := parseInterval(raw); ok {
			t.Errorf("parseInterval(%q) accepted an unparsable value", raw)
		}
	}
}

func TestAToolIntervalOutranksTheFleetInterval(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	h.env["AUTO_UPDATE_INTERVAL"] = "99d"
	h.env["DEMO_AUTO_UPDATE_INTERVAL"] = "1s"

	h.run(context.Background())
	h.now = h.now.Add(2 * time.Second)
	h.run(context.Background())

	if got := h.source.calls.Load(); got != 2 {
		t.Errorf("source consulted %d times, want 2", got)
	}
}

func TestAnUnparseableIntervalFallsBackToTheDefault(t *testing.T) {
	h := newHarness(t, "v2.0.0")
	h.env["AUTO_UPDATE_INTERVAL"] = "soon"

	if got := h.config.resolved().Interval; got != DefaultInterval {
		t.Errorf("interval = %v, want %v", got, DefaultInterval)
	}
}

func TestToolVariable(t *testing.T) {
	if got := toolVariable("my-tool", "NO_AUTO_UPDATE"); got != "MY_TOOL_NO_AUTO_UPDATE" {
		t.Errorf("toolVariable = %q", got)
	}
}
