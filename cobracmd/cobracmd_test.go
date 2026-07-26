package cobracmd_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/goselfupdate"
	"github.com/datapointchris/goselfupdate/cobracmd"
)

// stubSource serves a fixed release without any network involved.
type stubSource struct {
	tag       string
	changelog []string
	err       error
}

func (s stubSource) LatestRelease(context.Context) (goselfupdate.Release, error) {
	if s.err != nil {
		return goselfupdate.Release{}, s.err
	}
	return goselfupdate.Release{Tag: s.tag}, nil
}

func (s stubSource) Download(context.Context, goselfupdate.Asset) ([]byte, error) {
	return nil, errors.New("not used by --check")
}

func (s stubSource) Changelog(context.Context, string, string) ([]string, error) {
	return s.changelog, nil
}

func config(source goselfupdate.Source, version string) goselfupdate.Config {
	return goselfupdate.Config{Binary: "tool", Version: version, Source: source}
}

// run drives the command with --check. The install path resolves the running
// executable, which under test is the test binary, so it is exercised through
// the library's UpdateTo instead.
func run(t *testing.T, cfg goselfupdate.Config, options ...cobracmd.Options) (string, error) {
	t.Helper()

	var out bytes.Buffer
	cmd := cobracmd.New(cfg, options...)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--check"})

	err := cmd.Execute()
	return out.String(), err
}

func TestReportsAvailableUpdateWithChangelog(t *testing.T) {
	source := stubSource{
		tag:       "v2.0.0",
		changelog: []string{"feat: add a thing", "fix: repair another"},
	}

	out, err := run(t, config(source, "v1.0.0"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, want := range []string{
		"✓ tool update available: v1.0.0 → v2.0.0",
		"Changes:",
		"• feat: add a thing",
		"• fix: repair another",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReportsUpToDate(t *testing.T) {
	out, err := run(t, config(stubSource{tag: "v1.0.0"}, "v1.0.0"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "✓ tool already at latest: v1.0.0") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if strings.Contains(out, "Changes:") {
		t.Errorf("printed a changelog with no update:\n%s", out)
	}
}

func TestSkipChangelogOption(t *testing.T) {
	source := stubSource{tag: "v2.0.0", changelog: []string{"feat: add a thing"}}

	out, err := run(t, config(source, "v1.0.0"), cobracmd.Options{SkipChangelog: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "Changes:") {
		t.Errorf("changelog printed despite SkipChangelog:\n%s", out)
	}
	if !strings.Contains(out, "update available") {
		t.Errorf("update not reported:\n%s", out)
	}
}

// A failure is written once, in the ✗ format, and wrapped so a caller's own
// error printing can suppress the duplicate.
func TestFailureIsReportedOnceAndWrapped(t *testing.T) {
	out, err := run(t, config(stubSource{tag: "v2.0.0"}, "dev"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, cobracmd.ErrReported) {
		t.Errorf("error is not wrapped with ErrReported: %v", err)
	}
	if !errors.Is(err, goselfupdate.ErrDevBuild) {
		t.Errorf("underlying error was lost: %v", err)
	}

	if count := strings.Count(out, "✗"); count != 1 {
		t.Errorf("failure reported %d times:\n%s", count, out)
	}
	if !strings.Contains(out, "✗ tool upgrade failed:") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestSourceErrorIsSurfaced(t *testing.T) {
	sentinel := errors.New("network down")

	out, err := run(t, config(stubSource{err: sentinel}, "v1.0.0"))
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want the source error", err)
	}
	if !strings.Contains(out, "network down") {
		t.Errorf("output does not name the cause:\n%s", out)
	}
}

func TestDefaultNameAndAlias(t *testing.T) {
	cmd := cobracmd.New(config(stubSource{tag: "v1.0.0"}, "v1.0.0"))

	if cmd.Use != "update" {
		t.Errorf("Use = %q, want update", cmd.Use)
	}
	if !slices.Contains(cmd.Aliases, "upgrade") {
		t.Errorf("Aliases = %v, want to contain upgrade", cmd.Aliases)
	}
	// Cobra derives Name from Use, so an alias invocation still reports the
	// canonical name. Callers keying off cmd.Name() depend on this.
	if cmd.Name() != "update" {
		t.Errorf("Name() = %q, want update", cmd.Name())
	}
}

func TestOptionsOverrideNameAndAliases(t *testing.T) {
	cmd := cobracmd.New(
		config(stubSource{tag: "v1.0.0"}, "v1.0.0"),
		cobracmd.Options{Use: "self-update", Aliases: []string{"selfup"}},
	)

	if cmd.Use != "self-update" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if !slices.Contains(cmd.Aliases, "selfup") || slices.Contains(cmd.Aliases, "upgrade") {
		t.Errorf("Aliases = %v", cmd.Aliases)
	}
}

// Cobra must not print the error itself, or every failure appears twice.
func TestErrorsAreSilencedOnTheCommand(t *testing.T) {
	cmd := cobracmd.New(config(stubSource{tag: "v1.0.0"}, "v1.0.0"))
	if !cmd.SilenceErrors {
		t.Error("SilenceErrors is not set")
	}
	if !cmd.SilenceUsage {
		t.Error("SilenceUsage is not set")
	}
}
