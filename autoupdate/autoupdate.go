package autoupdate

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/goselfupdate"
)

// DefaultInterval is how long a check is good for.
const DefaultInterval = 24 * time.Hour

// DefaultTimeout bounds the check. Start gives the check a context with this
// deadline, and Finish waits at most this long for a check still in flight. A
// command that outlasts the check never waits for it.
const DefaultTimeout = 5 * time.Second

// The environment variables every tool reads, whatever it is called. Each also
// has a per-tool form that toolVariable builds by prefixing the tool's name:
// MY_TOOL_NO_AUTO_UPDATE for a tool named my-tool.
//
// The disable variable is presence-only, any value including empty -- the
// NO_COLOR convention -- so that NO_AUTO_UPDATE=0 cannot mean "on". The
// interval gets its own name precisely so presence and value never have to
// share one variable.
const (
	sharedDisableVariable  = "NO_AUTO_UPDATE"
	sharedIntervalVariable = "AUTO_UPDATE_INTERVAL"
)

// ciVariables are the ones gh checks, plus the two GitHub sets itself.
var ciVariables = []string{"CI", "BUILD_NUMBER", "RUN_ID", "GITHUB_ACTIONS", "CODESPACES"}

// Skip explains why a check did not happen.
type Skip string

const (
	SkipNone       Skip = ""
	SkipDisabled   Skip = "disabled"
	SkipDevBuild   Skip = "dev-build"
	SkipNotATTY    Skip = "not-a-tty"
	SkipCI         Skip = "ci"
	SkipInterval   Skip = "interval"
	SkipFailed     Skip = "failed"
	SkipSuppressed Skip = "suppressed"
)

// Config describes one updatable tool.
type Config struct {
	// Update is the underlying update configuration. Required.
	Update goselfupdate.Config

	// Tool names the state directory and the per-tool environment variables.
	// Defaults to Update.Binary.
	Tool string

	// Interval between checks. Zero means DefaultInterval.
	Interval time.Duration

	// Timeout bounds the check. Zero means DefaultTimeout.
	Timeout time.Duration

	// Out receives the one line this package is allowed to print. Defaults to
	// os.Stderr.
	Out io.Writer

	// Interactive overrides terminal detection. Nil detects. Set it to false
	// from a program that already knows it is writing somewhere a human will
	// not read -- into a pager, a log, a structured-output mode -- since
	// nothing about the streams themselves reveals that.
	Interactive *bool

	// Suppress skips the check unconditionally. Intended for a command that
	// must never trigger one: an update command, a shell-completion callback
	// firing on every TAB press, anything a script parses.
	Suppress bool

	// StateDir overrides where state is written. Defaults to [StateHome].
	StateDir string

	// Clock supplies the current time. Defaults to time.Now.
	Clock func() time.Time

	// Environ looks up an environment variable, as os.LookupEnv does.
	Environ func(string) (string, bool)
}

// Outcome reports what happened. Returned for tests and dashboards, not for
// control flow: a caller has nothing useful to do with it.
type Outcome struct {
	Checked bool
	Skip    Skip
	Current string
	Latest  string
}

// UpdateAvailable reports whether the check found a newer release.
func (o Outcome) UpdateAvailable() bool {
	return o.Latest != "" && o.Latest != o.Current
}

// Session is an in-flight check.
type Session struct {
	config   Config
	cancel   context.CancelFunc
	results  chan Outcome
	outcome  Outcome
	finished bool
}

// Start runs the gate and, if it passes, begins a check in the background.
//
// It returns immediately and never blocks the command about to run. Call
// [Session.Finish] once that command has finished.
func Start(ctx context.Context, config Config) *Session {
	config = config.resolved()

	session := &Session{config: config}

	if skip := gate(config); skip != SkipNone {
		session.outcome = Outcome{Skip: skip}
		session.finished = true
		return session
	}

	// Stamped before the network call, not after. gh stamps only on success, so
	// a rate-limited or offline user re-hits the API on every single invocation
	// until the window resets. The whole point of an interval is to bound the
	// request rate, and only this ordering actually does that.
	stored := readStateFrom(config)
	stored.Tool = config.Tool
	stored.CurrentVersion = goselfupdate.Canonical(config.Update.Version)

	// Stamped in place, not into a copy. The goroutine below writes this same
	// value again once the check returns, so stamping a throwaway copy here
	// would have the later write clobber the timestamp with a zero -- silently
	// undoing the ordering this comment exists to protect.
	stored = stamp(stored, config.Clock())
	writeState(config.StateDir, stored)

	checkCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	session.cancel = cancel
	session.results = make(chan Outcome, 1)

	go func() {
		defer cancel()
		session.results <- runCheck(checkCtx, config, stored)
	}()

	return session
}

// Finish waits for a check still in flight, for at most Config.Timeout, then
// prints the notice, if there is one, and returns what happened.
//
// It never returns an error and never panics: an update notice must not be able
// to break the command the user actually typed. Safe to call more than once.
func (s *Session) Finish() Outcome {
	if s.finished {
		return s.outcome
	}
	s.finished = true

	select {
	case outcome := <-s.results:
		s.outcome = outcome
	case <-time.After(s.config.Timeout):
		// The check is still running a full timeout after the command finished.
		// Its context carries a deadline set at Start, which ends a check that
		// honors it sooner. What reaches this branch ignores the deadline: a
		// token command on its own timeout, or a Source that never reads its
		// context. Cancel it rather than make the user wait; the timestamp was
		// already written, so this costs at most one skipped interval.
		s.cancel()
		s.outcome = Outcome{Skip: SkipFailed}
		return s.outcome
	}

	if s.outcome.UpdateAvailable() {
		_, _ = fmt.Fprintf(s.config.Out, "%s %s available (running %s) — run `%s update`\n",
			s.config.Tool, s.outcome.Latest, s.outcome.Current, s.config.Tool)
	}
	return s.outcome
}

// Enabled reports whether the tool checks at all, and the [Skip] reason when it
// does not, which is what explains a missing notice. It runs every step of
// Start's gate except the interval: it answers "is this tool opted in", not "is
// it due", as pyselfupdate and bashselfupdate do. It reads no clock, no state
// file and no network.
func Enabled(config Config) (bool, Skip) {
	skip := optedIn(config.resolved())
	return skip == SkipNone, skip
}

func runCheck(ctx context.Context, config Config, stored State) Outcome {
	result, err := goselfupdate.Check(ctx, config.Update)
	if err != nil {
		stored.LastError = err.Error()
		writeState(config.StateDir, stored)
		return Outcome{Skip: SkipFailed}
	}

	stored.LastError = ""
	stored.CurrentVersion = result.From
	stored.LatestVersion = result.To
	writeState(config.StateDir, stored)

	return Outcome{Checked: true, Current: result.From, Latest: result.To}
}

// gate decides whether a check may run. Every step is local; nothing here
// costs I/O beyond reading the state file for the interval.
func gate(config Config) Skip {
	if skip := optedIn(config); skip != SkipNone {
		return skip
	}

	stored := readStateFrom(config)
	if stored.CheckedAtEpoch > 0 {
		elapsed := config.Clock().Sub(time.Unix(stored.CheckedAtEpoch, 0))
		if elapsed < config.Interval {
			return SkipInterval
		}
	}

	return SkipNone
}

// optedIn is the gate without the interval, so it reads only the config, the
// environment and the streams.
func optedIn(config Config) Skip {
	if config.Suppress {
		return SkipSuppressed
	}
	if _, ok := config.Environ(sharedDisableVariable); ok {
		return SkipDisabled
	}
	if _, ok := config.Environ(toolVariable(config.Tool, sharedDisableVariable)); ok {
		return SkipDisabled
	}

	// Evaluated locally, before any network call. This is the whole answer to a
	// development build: it never reaches GitHub, so there is never an error to
	// swallow. Note that a Go build pseudo-version is itself valid semver and
	// would pass a naive parse, which is why the check is for a plain release
	// version rather than merely a parseable one.
	if !goselfupdate.IsReleaseVersion(config.Update.Version) {
		return SkipDevBuild
	}

	if !isInteractive(config) {
		return SkipNotATTY
	}
	for _, name := range ciVariables {
		if _, ok := config.Environ(name); ok {
			return SkipCI
		}
	}

	return SkipNone
}

// isInteractive requires both streams to be terminals.
//
// Checking both matters: `tool list | jq` should arguably still be allowed to
// notify on stderr, but `tool list > file 2>&1` must not, or the notice lands
// in the file. Requiring both is the conservative reading and matches gh.
func isInteractive(config Config) bool {
	if config.Interactive != nil {
		return *config.Interactive
	}
	return isTerminal(os.Stdout) && isTerminal(os.Stderr)
}

// isTerminal avoids a dependency on x/term, which would cost this package the
// zero-dependency guarantee the whole library is built around.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func readStateFrom(config Config) State {
	return readStateAt(config.StateDir, config.Tool)
}

func toolVariable(tool, suffix string) string {
	normalised := strings.ToUpper(strings.ReplaceAll(tool, "-", "_"))
	return normalised + "_" + suffix
}

// parseInterval accepts 30s, 45m, 12h, 7d, or a bare number of seconds.
//
// Not time.ParseDuration, which rejects "7d" -- and a day is the unit anyone
// setting this will reach for first.
func parseInterval(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, false
	}

	unit := time.Second
	switch raw[len(raw)-1] {
	case 's':
		raw = raw[:len(raw)-1]
	case 'm':
		unit = time.Minute
		raw = raw[:len(raw)-1]
	case 'h':
		unit = time.Hour
		raw = raw[:len(raw)-1]
	case 'd':
		unit = 24 * time.Hour
		raw = raw[:len(raw)-1]
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return time.Duration(value * float64(unit)), true
}

func (c Config) resolved() Config {
	if c.Tool == "" {
		c.Tool = c.Update.Binary
	}
	if c.Out == nil {
		c.Out = os.Stderr
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if c.Environ == nil {
		c.Environ = os.LookupEnv
	}
	if c.StateDir == "" {
		c.StateDir = StateHome()
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Interval <= 0 {
		c.Interval = c.intervalFromEnv()
	}
	return c
}

func (c Config) intervalFromEnv() time.Duration {
	environ := c.Environ
	if environ == nil {
		environ = os.LookupEnv
	}
	tool := c.Tool
	if tool == "" {
		tool = c.Update.Binary
	}

	for _, name := range []string{toolVariable(tool, sharedIntervalVariable), sharedIntervalVariable} {
		if raw, ok := environ(name); ok {
			if parsed, valid := parseInterval(raw); valid {
				return parsed
			}
		}
	}
	return DefaultInterval
}
