// Package version exposes build metadata injected at link time.
package version

// Set via -ldflags "-X github.com/KaanEmec/vitamux/internal/version.Version=… -X …Commit=…".
var (
	Version = "dev"
	Commit  = "unknown"
)
