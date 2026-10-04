package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/KaanEmec/vitamux/internal/connectors/remote"
)

const connectorTestUsage = `usage: vitamux connector-test --url URL --secret-file FILE [--scenario FILE]

Checks a sidecar connector against protocol vitamux-connector/1 (docs/sidecars.md): headers,
describe, auth steps, paging and cursors, idempotent replay, rotated credentials, typed errors
and that no secret is echoed. Each check prints PASS, FAIL or SKIP with one sentence; the exit
status is 1 when any check fails. FILE holds the sidecar's shared secret.

--scenario is a JSON file of synthetic sign-in values per check (schemas/connector-test-scenario.v1.json,
example: examples/sidecar-python/conformance.json). Without it only the first auth step is checked
and fetch runs with placeholder credentials, which a sidecar in REPLAY=1 mode serves.
`

func connectorTestCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("connector-test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, connectorTestUsage) }
	url := fs.String("url", "", "base URL of the sidecar, e.g. http://127.0.0.1:8080")
	secretFile := fs.String("secret-file", "", "file with the sidecar's shared secret")
	scenarioFile := fs.String("scenario", "", "scenario JSON file with synthetic sign-in values")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *url == "" || *secretFile == "" || fs.NArg() != 0 {
		fmt.Fprint(stderr, connectorTestUsage)
		return 2
	}
	secret, err := os.ReadFile(*secretFile)
	if err != nil {
		fmt.Fprintf(stderr, "connector-test: %v\n", err)
		return 1
	}
	var scn remote.Scenario
	if *scenarioFile != "" {
		if scn, err = remote.LoadScenario(*scenarioFile); err != nil {
			fmt.Fprintf(stderr, "connector-test: %v\n", err)
			return 1
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := remote.ConformanceOptions{URL: *url, Secret: strings.TrimSpace(string(secret)), Scenario: scn}
	failed, err := remote.RunConformance(ctx, opts, func(r remote.ConformanceResult) {
		fmt.Fprintf(stdout, "%-4s  %-20s %s\n", r.Status, r.Check, r.Message)
	})
	if err != nil {
		fmt.Fprintf(stderr, "connector-test: %v\n", err)
		return 1
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "%d check(s) failed\n", failed)
		return 1
	}
	return 0
}
