package goselfupdate

import (
	"os"
	"testing"
)

// TestMain empties the token command for the whole package.
//
// The default is `gh auth token`, so without this the suite reads whoever is
// running it: it passes or fails on whether they happen to be logged in, and
// their credential reaches assertion output on failure. A test that wants the
// default asks for it with t.Setenv, and one that wants a token sets the
// variable to a command that prints one.
func TestMain(m *testing.M) {
	if err := os.Setenv(TokenCommandEnv, ""); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
