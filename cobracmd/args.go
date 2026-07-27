package cobracmd

import "os"

// commandLineArgs is os.Args without the program name.
//
// Its own file and function so a test can reason about Execute without an
// os.Args round trip.
func commandLineArgs() []string {
	if len(os.Args) < 2 {
		return nil
	}
	return os.Args[1:]
}
