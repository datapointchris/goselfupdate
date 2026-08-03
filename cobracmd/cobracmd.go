// Package cobracmd provides a ready-made update command for CLIs built with
// [cobra].
//
// It is a separate package so that importing goselfupdate does not impose a
// CLI framework on projects using flag, urfave/cli or anything else.
//
//	root.AddCommand(cobracmd.New(goselfupdate.Config{
//		Owner:   "datapointchris",
//		Repo:    "todoui",
//		Binary:  "todoui",
//		Version: version,
//	}))
//
// [cobra]: https://github.com/spf13/cobra
package cobracmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/datapointchris/goselfupdate"
)

// ErrReported wraps every error [New]'s command returns, marking it as already
// written to stderr. A program whose main prints the error returned by
// Execute should skip anything matching this, otherwise the failure is
// reported twice:
//
//	if err := root.Execute(); err != nil {
//		if !errors.Is(err, cobracmd.ErrReported) {
//			fmt.Fprintln(os.Stderr, "error:", err)
//		}
//		os.Exit(1)
//	}
//
// Cobra's own error printing is already suppressed on the command.
var ErrReported = errors.New("already reported")

// Options adjusts the generated command.
type Options struct {
	// Use overrides the command name. Defaults to "update".
	Use string

	// Aliases overrides the command's aliases. Defaults to "upgrade".
	Aliases []string

	// Changelog prints the commits between the two versions after a
	// successful update. Enabled by default; it costs one extra request and is
	// skipped silently when the source cannot produce one.
	SkipChangelog bool
}

// New returns an update command for cfg.
func New(cfg goselfupdate.Config, options ...Options) *cobra.Command {
	opts := Options{}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Use == "" {
		opts.Use = "update"
	}
	if opts.Aliases == nil {
		opts.Aliases = []string{"upgrade"}
	}

	var checkOnly bool

	cmd := &cobra.Command{
		Use:     opts.Use,
		Aliases: opts.Aliases,
		Short:   fmt.Sprintf("Update %s to the latest release", cfg.Binary),
		Long: fmt.Sprintf(`Download and install the latest %s release from GitHub.

Replaces the running binary with a pre-built release archive, verified against
the release's published checksums. No Go toolchain required.`, cfg.Binary),
		Args: cobra.NoArgs,
		// The command writes its own failures in the ✓/✗ format below, so
		// neither cobra nor the caller's main should print them again.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			run := goselfupdate.Update
			if checkOnly {
				run = goselfupdate.Check
			}

			result, err := run(cmd.Context(), cfg)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "✗ %s upgrade failed: %s\n", cfg.Binary, err)
				return fmt.Errorf("%w: %w", ErrReported, err)
			}

			report(cmd.Context(), cmd.OutOrStdout(), cfg, opts, result)
			return nil
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false,
		"report whether an update is available without installing it")

	return cmd
}

func report(ctx context.Context, out io.Writer, cfg goselfupdate.Config, opts Options, result goselfupdate.Result) {
	if !result.UpdateAvailable() {
		_, _ = fmt.Fprintf(out, "✓ %s already at latest: %s\n", cfg.Binary, result.To)
		return
	}

	if result.Applied {
		_, _ = fmt.Fprintf(out, "✓ %s upgraded: %s → %s\n", cfg.Binary, result.From, result.To)
	} else {
		_, _ = fmt.Fprintf(out, "✓ %s update available: %s → %s\n", cfg.Binary, result.From, result.To)
	}

	if opts.SkipChangelog {
		return
	}

	subjects, err := goselfupdate.Changelog(ctx, cfg, result.From, result.To)
	if err != nil || len(subjects) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Changes:")
	for _, subject := range subjects {
		_, _ = fmt.Fprintf(out, "  • %s\n", subject)
	}
}
