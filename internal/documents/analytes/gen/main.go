// Command gen writes the files generated from internal/documents/analytes: the analyte seed
// migration and docs/analytes.md. Run it from the repository root: go run ./internal/documents/analytes/gen
package main

import (
	"fmt"
	"os"

	"github.com/KaanEmec/vitamux/internal/documents/analytes"
)

func main() {
	seed, err := analytes.SeedPath(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	for path, content := range map[string]string{seed: analytes.SeedSQL(), analytes.DocPath: analytes.Doc()} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // repository files, not secrets
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
	}
}
