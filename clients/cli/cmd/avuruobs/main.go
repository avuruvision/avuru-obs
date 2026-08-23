// Command avuruobs is the Avuru Obs command-line client.
//
// It exists to keep one claim true: the Hub API is the client-agnostic
// contract, and the web app is one client of it. Everything here goes through
// the public, versioned API with a personal API token — no private endpoints,
// no direct database access.
//
// Deliberately dependency-free. A binary people `go install` and hand an API
// token deserves a supply chain they can read in an afternoon, and the standard
// library covers flags, HTTP and table alignment. If a real command framework
// becomes worth it, that is a change to this file alone.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/avuru/avuru-obs/clients/cli/internal/client"
	"github.com/avuru/avuru-obs/clients/cli/internal/config"
)

// Version is stamped at build time (-ldflags "-X main.Version=…").
var Version = "dev"

// Exit codes are part of the contract, because a pipeline has to tell "the
// gate tripped" from "the gate could not run" — a single non-zero exit cannot.
const (
	exitOK        = 0
	exitError     = 1
	exitPredicate = 2
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitError
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "login":
		err = cmdLogin(rest)
	case "services":
		err = cmdServices(rest)
	case "health":
		err = cmdHealth(rest)
	case "traces":
		err = cmdTraces(rest)
	case "logs":
		err = cmdLogs(rest)
	case "status":
		err = cmdStatus(rest)
	case "version":
		fmt.Println(Version)
		return exitOK
	case "help", "-h", "--help":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "avuruobs: unknown command %q\n\n", cmd)
		usage()
		return exitError
	}

	var pe *predicateMatched
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &pe):
		fmt.Fprintf(os.Stderr, "avuruobs: %s\n", pe.Error())
		return exitPredicate
	default:
		fmt.Fprintf(os.Stderr, "avuruobs: %s\n", err)
		return exitError
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `avuruobs — the Avuru Obs command-line client

  avuruobs login --url <hub-url> --token <avurut_…>
  avuruobs services [--window 15m] [--fail-on 'errorRate>0.05']
  avuruobs health   [--window 15m] [--fail-on 'status!=healthy']
  avuruobs traces   [--service NAME] [--status error] [--limit 20]
  avuruobs logs     [--service NAME] [--severity ERROR] [--query TEXT]
  avuruobs status
  avuruobs version

Common flags: -o table|json, --project NAME, --timeout 30s

Exit codes: 0 nothing matched · 1 the command failed · 2 --fail-on matched.
`)
}

// predicateMatched is not a failure of the command — the command worked and the
// answer was "yes". It carries its own exit code so a caller can react to the
// gate without parsing output.
type predicateMatched struct{ detail string }

func (p *predicateMatched) Error() string { return p.detail }

// commonFlags are shared by every read command.
type commonFlags struct {
	output  *string
	project *string
	window  *time.Duration
	failOn  *string
	timeout *time.Duration
}

func addCommon(fs *flag.FlagSet) commonFlags {
	return commonFlags{
		output:  fs.String("o", "table", "output format: table|json"),
		project: fs.String("project", "", "project (tenant) to read; defaults to the token owner's default"),
		window:  fs.Duration("window", 15*time.Minute, "how far back to look"),
		failOn:  fs.String("fail-on", "", "exit 2 when a row matches, e.g. 'errorRate>0.05'"),
		timeout: fs.Duration("timeout", 30*time.Second, "HTTP timeout"),
	}
}

func (c commonFlags) params() url.Values {
	now := time.Now().UTC()
	v := url.Values{}
	v.Set("start", now.Add(-*c.window).Format(time.RFC3339))
	v.Set("end", now.Format(time.RFC3339))
	v.Set("windowSec", fmt.Sprintf("%d", int(c.window.Seconds())))
	return v
}

func newClient(c commonFlags) (*client.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return client.New(cfg, *c.timeout, *c.project), nil
}
