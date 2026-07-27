package cobracmd

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/datapointchris/goselfupdate/autoupdate"
)

// neverAutoUpdate names commands that must never trigger an update check.
//
// The critical entry is cobra's shell-completion callback: it runs on **every
// TAB press**, so a check there would fire constantly and add latency to
// something that must feel instant. `update` is listed because a check racing
// the update it is about to perform is pointless, and `version` because it is
// what a script calls to find out what is installed.
var neverAutoUpdate = map[string]bool{
	"update":                        true,
	"upgrade":                       true,
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
//			os.Exit(1)
//		}
//	}
//
// Deliberately not a PersistentPreRun: cobra runs only the *closest*
// PersistentPreRunE in the ancestry, so a hook here would work for a root that
// has none and silently do nothing for one that does.
func Execute(ctx context.Context, root *cobra.Command, config autoupdate.Config) error {
	config.Suppress = config.Suppress || targetIsSuppressed(root)

	session := autoupdate.Start(ctx, config)
	err := root.ExecuteContext(ctx)
	session.Finish()
	return err
}

// targetIsSuppressed resolves which command will actually run and asks whether
// it is on the list.
//
// Resolved with cobra's own Find so the answer matches what cobra will do with
// flags, aliases and abbreviations, rather than a hand-rolled scan of os.Args
// that would have to reimplement all three.
func targetIsSuppressed(root *cobra.Command) bool {
	args := root.Flags().Args()
	if len(args) == 0 {
		args = commandLineArgs()
	}

	target, _, err := root.Find(args)
	if err != nil || target == nil {
		// An unresolvable command line is about to produce a usage error, which
		// is not the moment for an update notice.
		return true
	}
	return suppressed(target)
}
