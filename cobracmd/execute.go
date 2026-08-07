package cobracmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datapointchris/goselfupdate/autoupdate"
)

// ErrUsage marks a failure caused by how the command was typed rather than by
// the command running and failing: an unknown or malformed flag, or an unknown
// subcommand. Select exit code 2 for it, as the shell convention and Python's
// argparse do, so a caller can tell "you typed it wrong" from "it ran and
// failed" -- only the former is worth retrying with different arguments.
//
// Cobra reports both as ordinary errors, which is what flattens every failure
// to exit 1 without this.
//
// Argument-count and custom [cobra.Command.Args] validation failures are not
// classified: cobra returns them as plain errors indistinguishable from a
// RunE failure without matching on message text, which callers must never
// have to do.
var ErrUsage = errors.New("usage error")

// neverAutoUpdate names commands that must never trigger an update check.
//
// The critical entry is cobra's shell-completion callback: it runs on **every
// TAB press**, so a check there would fire constantly and add latency to
// something that must feel instant. `update` is listed because a check racing
// the update it is about to perform is pointless, and `version` because it is
// what a script calls to find out what is installed.
var neverAutoUpdate = map[string]bool{
	"update":                        true,
	"version":                       true,
	"completion":                    true,
	"help":                          true,
	cobra.ShellCompRequestCmd:       true,
	cobra.ShellCompNoDescRequestCmd: true,
}

// suppressed reports whether a command, or anything it is nested under, is on
// the list.
//
// The ancestry is walked rather than only the leaf because `completion` owns a
// subcommand per shell: `tool completion zsh` has leaf name "zsh", and a
// leaf-only check misses it entirely. That is not hypothetical -- todoui hit
// exactly this with its sync-on-start, and every shell that sourced the
// completions paid for it.
func suppressed(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if neverAutoUpdate[current.Name()] {
			return true
		}
	}
	return false
}

// Execute runs root with an update check racing alongside it.
//
// The check starts before the command and the notice prints after it, so a fast
// command pays nothing and the line is not buried in the command's output. This
// is gh's shape, and it is the reason there is no blocking mode.
//
//	func main() {
//		if err := cobracmd.Execute(context.Background(), rootCmd, autoConfig()); err != nil {
//			if !errors.Is(err, cobracmd.ErrReported) {
//				fmt.Fprintln(os.Stderr, err)
//			}
//			if errors.Is(err, cobracmd.ErrUsage) {
//				os.Exit(2)
//			}
//			os.Exit(1)
//		}
//	}
//
// Deliberately not a PersistentPreRun: cobra runs only the *closest*
// PersistentPreRunE in the ancestry, so a hook here would work for a root that
// has none and silently do nothing for one that does.
func Execute(ctx context.Context, root *cobra.Command, config autoupdate.Config) error {
	// An unresolvable command line is about to produce a usage error, which is
	// not the moment for an update notice.
	target, findErr := resolveTarget(root)
	config.Suppress = config.Suppress || findErr != nil || target == nil || suppressed(target)

	// Composed with whatever the caller already set rather than replacing it:
	// cobra hands back a working default when nothing is set, so this is safe
	// either way and never silently drops a consumer's own handler.
	previous := root.FlagErrorFunc()
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w: %w", ErrUsage, previous(cmd, err))
	})

	session := autoupdate.Start(ctx, config)
	err := root.ExecuteContext(ctx)
	session.Finish()

	// findErr is cobra's own verdict that the command line names nothing it can
	// run, so any error from a line it already rejected is a usage error.
	if err != nil && findErr != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	return err
}

// resolveTarget asks cobra which command the command line will actually run.
//
// Resolved with cobra's own Find so the answer matches what cobra will do with
// flags, aliases and abbreviations, rather than a hand-rolled scan of os.Args
// that would have to reimplement all three.
func resolveTarget(root *cobra.Command) (*cobra.Command, error) {
	args := root.Flags().Args()
	if len(args) == 0 {
		args = commandLineArgs()
	}

	target, _, err := root.Find(args)
	return target, err
}
