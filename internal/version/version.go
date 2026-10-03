// Package version exposes build metadata injected at link time.
package version

// Set via -ldflags "-X github.com/KaanEmec/vitamux/internal/version.Version=… -X …Commit=…".
var (
	Version = "dev"
	Commit  = "unknown"
)

// SchemaVersion is the database schema version this binary expects.
// It is bumped together with migrations (J02.1); 0 means "no schema yet".
const SchemaVersion = 0
