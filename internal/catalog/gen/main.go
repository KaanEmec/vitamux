// Command gen writes the files generated from internal/catalog: the seed migrations (one per catalogue marker) and
// docs/metrics.md. Run it from the repository root: go run ./internal/catalog/gen
package main

import (
	"fmt"
	"os"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

func main() {
	files := catalog.SeedFiles()
	files[catalog.DocPath] = catalog.MetricsDoc()
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // repository files, not secrets
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
	}
}
