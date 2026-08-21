package goselfupdate

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// TokenCommandEnv names the command that produces a GitHub credential.
const TokenCommandEnv = "GITHUB_TOKEN_COMMAND"

// DefaultTokenCommand runs when nothing overrides TokenCommandEnv.
//
// Authenticating is the default because the alternative is not "no credential"
// but sixty requests an hour, charged per IP address and shared by every host
// behind one egress. Measured 2026-08-21 across one household: two machines
// checking on a timer held that pool at zero for whole hours, and every tool on
// the network that asked anonymously was refused. A default that has to be
// opted into is a default nobody sets.
const DefaultTokenCommand = "gh auth token"

// tokenCommandTimeout bounds the helper. gh is a local read, but the variable
// takes an arbitrary command and a password manager can block on a locked vault
// or a touch prompt nobody is there to answer — and the check runs on a timer.
const tokenCommandTimeout = 10 * time.Second

func tokenFromEnv() string {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// tokenFromCommand runs $GITHUB_TOKEN_COMMAND, or `gh auth token`.
//
// One lever that both redirects and disables, which is what a switch has to do
// to be worth having. Unset runs the default; set to a command runs that one;
// set to empty runs nothing and the request goes out unauthenticated.
//
//	GITHUB_TOKEN_COMMAND='pass show github/token'
//	GITHUB_TOKEN_COMMAND=''
//
// Named for what it produces rather than for turning something off. A
// NO_GH_TOKEN cannot say "use this other source" and reads as a claim about
// whether one exists rather than an instruction about whether to use one.
//
// Never fails. No such command, a non-zero exit, a binary that is not installed
// — every one degrades to an unauthenticated request, which still works against
// a public repository.
func tokenFromCommand() string {
	command, ok := os.LookupEnv(TokenCommandEnv)
	if !ok {
		command = DefaultTokenCommand
	}
	argv := splitCommand(command)
	if len(argv) == 0 {
		return ""
	}
	binary, err := exec.LookPath(argv[0])
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), tokenCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, argv[1:]...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// splitCommand splits on whitespace, honoring single and double quotes.
//
// No shell runs the result. `sh -c` would handle quoting for free and is what
// git's credential.helper does, but it also brings pipelines, redirection and
// expansion into a value read on every update check — and pyselfupdate splits
// with shlex and executes directly, so a shell here would make the same
// variable mean different things in two languages.
//
// Quotes are honored rather than skipped because a vault path with a space in
// it is ordinary: `pass show "github/personal token"`.
func splitCommand(command string) []string {
	var (
		argv    []string
		current strings.Builder
		quote   rune
		started bool
	)
	for _, r := range command {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			started = true
		case quote == 0 && (r == ' ' || r == '\t' || r == '\n'):
			if started {
				argv = append(argv, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if started {
		argv = append(argv, current.String())
	}
	return argv
}
