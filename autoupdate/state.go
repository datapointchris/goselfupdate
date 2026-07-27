// Package autoupdate tells a user that a newer release exists, and nothing
// else.
//
// It never installs. [goselfupdate.Update] installs, and the command wrapping
// it is where errors are printed; a failure here is recorded in the state file
// and swallowed. That single rule is what keeps a development build from
// printing an upgrade failure on every invocation.
//
//	session := autoupdate.Start(ctx, autoupdate.Config{
//		Update: goselfupdate.Config{Owner: "you", Repo: "tool", Binary: "tool", Version: version},
//	})
//	err := rootCmd.ExecuteContext(ctx)
//	session.Finish()
//
// The check runs concurrently with the caller's own work and the notice is
// printed afterwards, so a fast command pays nothing and the line is not buried
// in the command's output.
//
// A separate package from goselfupdate so that a program wanting only the
// update half links none of this.
package autoupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Schema is the version of the on-disk state format.
//
// The same schema is written by pyselfupdate and bashselfupdate, so any tool
// can read any other tool's state and one dashboard can glob
// ~/.local/state/*/autoupdate.json with no per-tool knowledge. Adding a field
// is safe; renaming or repurposing one breaks the other two.
const Schema = 1

// StateFilename is the file written inside the per-tool state directory.
const StateFilename = "autoupdate.json"

// State is one tool's record of its last update check.
//
// This is state, not configuration and not cache: it persists across runs, it
// is not authored by the user, and deleting it changes behavior rather than
// merely costing a recompute. That is XDG_STATE_HOME by the Base Directory
// specification, and it is where gh puts the same thing.
type State struct {
	Schema int    `json:"schema"`
	Tool   string `json:"tool"`

	CheckedAt string `json:"checked_at"`

	// CheckedAtEpoch is the same instant as CheckedAt. Redundant on purpose:
	// BSD date on macOS cannot parse ISO-8601 without -j -f gymnastics, and the
	// bash implementation has to do interval arithmetic with jq and date +%s
	// alone. One duplicated field buys a portable bash sibling.
	CheckedAtEpoch int64 `json:"checked_at_epoch"`

	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`

	// LastError is non-empty when the last check failed. There is deliberately
	// no separate "skip reason" field: a gate that declines to check does not
	// write this file at all, which is what makes the absence of a state file
	// observable proof that the network was never touched.
	LastError string `json:"last_error"`
}

// StateHome is the directory state files live under, honoring XDG_STATE_HOME.
func StateHome() string {
	if override := os.Getenv("XDG_STATE_HOME"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "state")
	}
	return filepath.Join(home, ".local", "state")
}

// StatePath is where a tool's state file lives.
func StatePath(tool string) string {
	return filepath.Join(StateHome(), tool, StateFilename)
}

// ReadState returns a tool's state, or a zero value when it has never been
// written.
//
// A corrupt or unreadable file reads as empty rather than failing: the file is
// a throttle, and breaking a user's command because the throttle cannot be read
// would be worse than checking one extra time.
func ReadState(tool string) State {
	return readStateAt(StateHome(), tool)
}

// readStateAt is ReadState against an explicit directory, which is what makes
// the package testable without reassigning XDG_STATE_HOME -- a process-global
// mutation that is neither concurrency-safe nor honest.
func readStateAt(directory, tool string) State {
	data, err := os.ReadFile(filepath.Join(directory, tool, StateFilename))
	if err != nil {
		return State{Tool: tool}
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{Tool: tool}
	}
	state.Tool = tool
	return state
}

// writeState persists a tool's state atomically.
//
// Written to a temporary file in the same directory and renamed, so a reader
// sees either the whole previous file or the whole new one. Failures are
// swallowed for the same reason ReadState tolerates corruption.
func writeState(directory string, state State) {
	state.Schema = Schema

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')

	path := filepath.Join(directory, state.Tool, StateFilename)
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return
	}

	temporary, err := os.CreateTemp(parent, "."+StateFilename+".*")
	if err != nil {
		return
	}
	name := temporary.Name()

	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		_ = os.Remove(name)
		return
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(name)
		return
	}
	// 0600 matches gh, which writes the same kind of file.
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
	}
}

// stamp sets both timestamp fields to the same instant.
func stamp(state State, now time.Time) State {
	state.CheckedAt = now.UTC().Format("2006-01-02T15:04:05Z")
	state.CheckedAtEpoch = now.Unix()
	return state
}
