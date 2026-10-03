//go:build webui

// Package web provides the configurator SPA assets (ADR-0010).
// Build with `-tags webui` after `npm run build` to embed web/build.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var build embed.FS

// Assets returns the built SPA rooted at its output directory.
func Assets() fs.FS {
	sub, err := fs.Sub(build, "build")
	if err != nil {
		panic(err) // unreachable: "build" is embedded at compile time
	}
	return sub
}

// Embedded reports whether real UI assets are compiled in.
const Embedded = true
