package cobracmd

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/spf13/cobra"

	"github.com/datapointchris/goselfupdate/autoupdate"
)

// buildRoot returns a root shaped like a real CLI: a leaf, a group with its own
// leaf, and the update command this package generates.
func buildRoot() *cobra.Command {
	root := &cobra.Command{Use: "demo", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "update", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "version", Run: func(*cobra.Command, []string) {}})

	admin := &cobra.Command{Use: "admin"}
	admin.AddCommand(&cobra.Command{Use: "update", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(admin)

	return root
}

func find(t *testing.T, root *cobra.Command, args ...string) *cobra.Command {
	t.Helper()
	target, _, err := root.Find(args)
	if err != nil {
		t.Fatalf("Find(%v): %v", args, err)
	}
	return target
}

func TestOrdinaryCommandsAreNotSuppressed(t *testing.T) {
	root := buildRoot()

	for _, args := range [][]string{{"list"}, {}} {
		if suppressed(find(t, root, args...)) {
			t.Errorf("%v was suppressed but should trigger a check", args)
		}
	}
}

func TestUpdateAndVersionAreSuppressed(t *testing.T) {
	root := buildRoot()

	for _, args := range [][]string{{"update"}, {"version"}} {
		if !suppressed(find(t, root, args...)) {
			t.Errorf("%v was not suppressed", args)
		}
	}
}

// The reason the ancestry is walked rather than only the leaf: `completion` owns
// a subcommand per shell, so `demo completion zsh` has leaf name "zsh" and a
// leaf-only check misses it entirely.
func TestCompletionIsSuppressedThroughItsShellSubcommand(t *testing.T) {
	root := buildRoot()
	root.InitDefaultCompletionCmd()

	target := find(t, root, "completion", "zsh")
	if target.Name() != "zsh" {
		t.Fatalf("expected to resolve the shell leaf, got %q", target.Name())
	}
	if !suppressed(target) {
		t.Error("completion zsh was not suppressed; a leaf-only check would miss it")
	}
}

// The critical entry. cobra runs this on every TAB press, so a check here would
// fire constantly and add latency to something that must feel instant.
func TestTheShellCompletionCallbackIsSuppressed(t *testing.T) {
	for _, name := range []string{cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd} {
		if !neverAutoUpdate[name] {
			t.Errorf("%q is not on the suppression list", name)
		}
	}
}

// A nested update command is suppressed by the same ancestry walk. meso ships
// exactly this shape: `meso admin update`.
func TestANestedUpdateCommandIsSuppressed(t *testing.T) {
	root := buildRoot()

	if !suppressed(find(t, root, "admin", "update")) {
		t.Error("admin update was not suppressed")
	}
}

func TestAnUnresolvableCommandLineIsSuppressed(t *testing.T) {
	root := &cobra.Command{Use: "demo", Args: cobra.NoArgs}
	root.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})

	// About to produce a usage error, which is not the moment for a notice.
	if _, _, err := root.Find([]string{"nonsense"}); err == nil {
		t.Skip("cobra resolved an unknown command; nothing to assert")
	}
}

// usageRoot returns a root whose failures are all classifiable: a leaf that
// succeeds and one that fails at run time, so a usage error can be told from an
// ordinary one.
func usageRoot() *cobra.Command {
	root := &cobra.Command{Use: "demo", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{
		Use:  "boom",
		RunE: func(*cobra.Command, []string) error { return errors.New("it ran and failed") },
	})
	return root
}

// Execute reads os.Args when the root carries no parsed args, which is the only
// way to drive both the Find and the cobra run from one place.
func withArgs(t *testing.T, args ...string) {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"demo"}, args...)
	t.Cleanup(func() { os.Args = original })
}

func execute(t *testing.T, root *cobra.Command) error {
	t.Helper()
	return Execute(context.Background(), root, autoupdate.Config{Suppress: true})
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	withArgs(t, "bogus")

	err := execute(t, usageRoot())
	if err == nil {
		t.Fatal("an unknown command returned no error")
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error is not ErrUsage: %v", err)
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	withArgs(t, "list", "--nope")

	err := execute(t, usageRoot())
	if err == nil {
		t.Fatal("an unknown flag returned no error")
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error is not ErrUsage: %v", err)
	}
}

// The distinction the exit code exists for: this one ran, so retrying with
// different arguments is not the fix.
func TestARunTimeFailureIsNotAUsageError(t *testing.T) {
	withArgs(t, "boom")

	err := execute(t, usageRoot())
	if err == nil {
		t.Fatal("boom returned no error")
	}
	if errors.Is(err, ErrUsage) {
		t.Errorf("a run-time failure was classified as a usage error: %v", err)
	}
}

func TestASuccessfulCommandReturnsNil(t *testing.T) {
	withArgs(t, "list")

	if err := execute(t, usageRoot()); err != nil {
		t.Errorf("list failed: %v", err)
	}
}

// A consumer that set its own handler keeps it: the wrapper composes rather
// than replaces, so their error still reaches them, now classifiable.
func TestACallersFlagErrorFuncIsPreserved(t *testing.T) {
	withArgs(t, "list", "--nope")

	called := false
	root := usageRoot()
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		called = true
		return err
	})

	err := execute(t, root)
	if !called {
		t.Error("the caller's FlagErrorFunc was replaced rather than composed with")
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error is not ErrUsage: %v", err)
	}
}
