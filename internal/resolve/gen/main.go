// Command gen writes docs/resolution-defaults.md from the built-in rules in internal/resolve.
// Run it from the repository root: go run ./internal/resolve/gen
package main

import (
	"fmt"
	"os"

	"github.com/KaanEmec/vitamux/internal/resolve"
)

func main() {
	if err := os.WriteFile(resolve.DefaultsDocPath, []byte(resolve.DefaultsDoc()), 0o644); err != nil { //nolint:gosec // repository file, not a secret
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}
