// Package version exposes build metadata injected at link time.
package version

// Set via -ldflags "-X github.com/KaanEmec/vitamux/internal/version.Version=… -X …Commit=…".
var (
	Version = "dev"
	Commit  = "unknown"
)

// Client handshake (GET /api/v1/system/version, J22.3).
const (
	// APIVersion is raised on every change that breaks existing API clients, such as the app.
	APIVersion = 1
	// MinAppVersion is the oldest iOS app version (CFBundleShortVersionString) this server supports.
	MinAppVersion = "0.4.0"
)
