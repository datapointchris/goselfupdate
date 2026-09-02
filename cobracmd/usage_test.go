package cobracmd

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// searchRoot returns a root shaped like the tool the fleet measured this on: a
// leaf carrying several long flags, so a near match can be told from the rest
// of the flag set rather than from a set of one.
func searchRoot() *cobra.Command {
	root := &cobra.Command{Use: "demo", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	search := &cobra.Command{Use: "search", Run: func(*cobra.Command, []string) {}}
	search.Flags().Bool("owned", false, "only owned corpora")
	search.Flags().Bool("reference", false, "only reference corpora")
	search.Flags().Int("limit", 10, "how many results")
	search.Flags().Bool("internal", false, "not for public use")
	_ = search.Flags().MarkHidden("internal")
	root.AddCommand(search)

	return root
}

func messageOf(t *testing.T, root *cobra.Command, args ...string) string {
	t.Helper()
	withArgs(t, args...)

	err := execute(t, root)
	if err == nil {
		t.Fatalf("%v returned no error", args)
	}
	return err.Error()
}

// The defect this package is the canonical fix for: the parser knows the flag
// set and reports only the token it rejected.
func TestAnUnknownFlagNamesTheNearMatches(t *testing.T) {
	message := messageOf(t, searchRoot(), "search", "--ownd")

	for _, want := range []string{"unknown flag: --ownd", "Did you mean this?", "--owned"} {
		if !strings.Contains(message, want) {
			t.Errorf("message does not carry %q:\n%s", want, message)
		}
	}
}

// Near matches, not the whole flag set. A wide flag surface would otherwise
// answer one typo with a wall, and the pointer names the command that prints
// all of them.
func TestAnUnknownFlagDoesNotNameEveryFlag(t *testing.T) {
	message := messageOf(t, searchRoot(), "search", "--ownd")

	for _, unwanted := range []string{"--reference", "--limit"} {
		if strings.Contains(message, unwanted) {
			t.Errorf("message names the whole flag set, including %q:\n%s", unwanted, message)
		}
	}
}

// A hidden flag is not part of the surface a mistyped line was aiming at.
func TestAHiddenFlagIsNeverSuggested(t *testing.T) {
	message := messageOf(t, searchRoot(), "search", "--internl")

	if strings.Contains(message, "--internal") {
		t.Errorf("a hidden flag was suggested:\n%s", message)
	}
}

// The third thing an error owes: the one command that changes the situation,
// naming the command that was actually being run rather than the root.
func TestAnUnknownFlagNamesTheCommandThatListsThem(t *testing.T) {
	message := messageOf(t, searchRoot(), "search", "--ownd")

	if want := "Run 'demo search --help' for usage."; !strings.Contains(message, want) {
		t.Errorf("message does not carry %q:\n%s", want, message)
	}
}

// Nothing close is still worth answering, because the pointer is what lists
// the alternatives when no guess is good enough to offer.
func TestAFlagErrorWithNoNearMatchStillNamesTheNextCommand(t *testing.T) {
	message := messageOf(t, searchRoot(), "search", "--xyzzy")

	if strings.Contains(message, "Did you mean this?") {
		t.Errorf("a guess was offered for a token nothing resembles:\n%s", message)
	}
	if want := "Run 'demo search --help' for usage."; !strings.Contains(message, want) {
		t.Errorf("message does not carry %q:\n%s", want, message)
	}
}

// A shorthand is one character, so every long flag is within two edits of it
// and a suggestion list would be the whole flag set.
func TestAShorthandMistakeIsNotAnsweredWithGuesses(t *testing.T) {
	message := messageOf(t, searchRoot(), "search", "-q")

	if strings.Contains(message, "Did you mean this?") {
		t.Errorf("a shorthand was answered with long-flag guesses:\n%s", message)
	}
	if want := "Run 'demo search --help' for usage."; !strings.Contains(message, want) {
		t.Errorf("message does not carry %q:\n%s", want, message)
	}
}

// A consumer that turned suggestions off for commands meant it for the whole
// command line.
func TestDisableSuggestionsSilencesTheFlagGuesses(t *testing.T) {
	root := searchRoot()
	root.DisableSuggestions = true
	for _, cmd := range root.Commands() {
		cmd.DisableSuggestions = true
	}

	message := messageOf(t, root, "search", "--ownd")

	if strings.Contains(message, "Did you mean this?") {
		t.Errorf("suggestions survived DisableSuggestions:\n%s", message)
	}
	if want := "Run 'demo search --help' for usage."; !strings.Contains(message, want) {
		t.Errorf("the pointer is not a suggestion and should have survived:\n%s", message)
	}
}

// The defect measured between two tools sharing this bootstrap: one showed the
// pointer on an unknown command and the other did not.
func TestAnUnknownCommandNamesTheCommandThatListsThem(t *testing.T) {
	message := messageOf(t, searchRoot(), "wat")

	if want := "Run 'demo --help' for usage."; !strings.Contains(message, want) {
		t.Errorf("message does not carry %q:\n%s", want, message)
	}
}

// Cobra prints the pointer itself when the command has not silenced its error
// output, so adding it to the error there would print it twice.
func TestTheCommandPointerIsNotAddedWhenCobraPrintsItAlready(t *testing.T) {
	root := searchRoot()
	root.SilenceErrors = false

	message := messageOf(t, root, "wat")

	if strings.Contains(message, "for usage.") {
		t.Errorf("the pointer was added on top of cobra's own:\n%s", message)
	}
}

// Cobra's own suggestion block is left alone, so the two halves of a mistyped
// command line read alike rather than gaining a second "Did you mean".
func TestAnUnknownCommandKeepsCobrasOwnSuggestion(t *testing.T) {
	message := messageOf(t, searchRoot(), "sarch")

	if got := strings.Count(message, "Did you mean this?"); got != 1 {
		t.Errorf("expected one suggestion block, got %d:\n%s", got, message)
	}
	if !strings.Contains(message, "\tsearch") {
		t.Errorf("cobra's own suggestion was lost:\n%s", message)
	}
	if strings.Contains(message, "\n\n\n") {
		t.Errorf("cobra's trailing newline and the suffix left a two-line gap:\n%q", message)
	}
}

// Explaining the failure must not disturb what a caller matches on, which is
// the whole reason the prose is a suffix rather than a replacement.
func TestExplainingAFlagErrorKeepsTheErrorMatchable(t *testing.T) {
	withArgs(t, "search", "--ownd")

	type callerUsage struct{ error }

	root := searchRoot()
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return callerUsage{err} })

	err := execute(t, root)
	var theirs callerUsage
	if !errors.As(err, &theirs) {
		t.Errorf("the caller's error type was lost: %v", err)
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error is not ErrUsage: %v", err)
	}
}

func TestEditDistanceCountsSingleEdits(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"owned", "owned", 0},
		{"ownd", "owned", 1},
		{"Owned", "owned", 0},
		{"", "owned", 5},
		{"owned", "", 5},
		{"nope", "owned", 4},
		{"limit", "owned", 5},
	}

	for _, c := range cases {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
