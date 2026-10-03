//go:build !webui

package web

import (
	"io/fs"
	"testing/fstest"
)

// Assets returns a placeholder page for backend-only builds (no `webui` tag).
func Assets() fs.FS {
	return fstest.MapFS{"index.html": {Data: []byte(placeholder)}}
}

// Embedded reports whether real UI assets are compiled in.
const Embedded = false

const placeholder = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Vitamux</title></head>
<body><h1>Vitamux</h1><p>The web UI is not embedded in this build. Run <code>make build</code>.</p></body></html>
`
