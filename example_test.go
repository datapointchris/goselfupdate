package goselfupdate_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/datapointchris/goselfupdate"
)

// version is injected at build time, conventionally with
// -ldflags "-X main.version={{.Version}}".
var version = "v1.0.0"

func ExampleUpdate() {
	result, err := goselfupdate.Update(context.Background(), goselfupdate.Config{
		Owner:   "datapointchris",
		Repo:    "todoui",
		Binary:  "todoui",
		Version: version,
	})
	if err != nil {
		log.Fatal(err)
	}

	if result.Applied {
		fmt.Printf("updated %s → %s\n", result.From, result.To)
	} else {
		fmt.Printf("already at %s\n", result.To)
	}
}

func ExampleCheck() {
	result, err := goselfupdate.Check(context.Background(), goselfupdate.Config{
		Owner:   "datapointchris",
		Repo:    "todoui",
		Binary:  "todoui",
		Version: version,
	})
	if err != nil {
		log.Fatal(err)
	}

	if result.UpdateAvailable() {
		fmt.Printf("%s is available, running %s\n", result.To, result.From)
	}
}

// Errors are sentinels, so a caller can tell a development build or a rate
// limit apart from a genuine failure without matching on message text.
func ExampleUpdate_errorHandling() {
	_, err := goselfupdate.Update(context.Background(), goselfupdate.Config{
		Owner:   "datapointchris",
		Repo:    "todoui",
		Binary:  "todoui",
		Version: version,
	})

	switch {
	case err == nil:
	case errors.Is(err, goselfupdate.ErrDevBuild):
		fmt.Println("built from source; update it the way you built it")
	case errors.Is(err, goselfupdate.ErrNoAsset):
		fmt.Println("no build published for this platform")
	case errors.Is(err, goselfupdate.ErrChecksumMismatch):
		fmt.Println("download did not match its published checksum")
	default:
		fmt.Println("update failed:", err)
	}
}

// A token raises GitHub's unauthenticated limit of 60 requests an hour and is
// required for a private repository. $GITHUB_TOKEN and $GH_TOKEN are used
// automatically when Token is empty.
func ExampleConfig_privateRepository() {
	cfg := goselfupdate.Config{
		Owner:      "datapointchris",
		Repo:       "internal-tool",
		Binary:     "internal-tool",
		Version:    version,
		Token:      os.Getenv("MY_GITHUB_TOKEN"),
		HTTPClient: &http.Client{Timeout: 2 * time.Minute},
	}

	if _, err := goselfupdate.Update(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}

// Point APIBase at a GitHub Enterprise installation's API root to update from
// one.
func ExampleGitHubSource_enterprise() {
	cfg := goselfupdate.Config{
		Binary:  "internal-tool",
		Version: version,
		Source: &goselfupdate.GitHubSource{
			Owner:   "platform",
			Repo:    "internal-tool",
			APIBase: "https://github.example.com/api/v3",
			Token:   os.Getenv("GHE_TOKEN"),
		},
	}

	if _, err := goselfupdate.Update(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}
