// Command gen writes the files generated from internal/catalog: the 00010 seed migration and
// docs/metrics.md. Run it from the repository root: go run ./internal/catalog/gen
package main

import (
	"fmt"
	"os"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

func main() {
	for path, content := range map[string]string{
		catalog.SeedPath: catalog.SeedSQL(),
		catalog.DocPath:  catalog.MetricsDoc(),
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // repository files, not secrets
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
	}
}
