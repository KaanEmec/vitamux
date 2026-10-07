package api

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// baseCSP applies when the built page carries no CSP meta tag (placeholder builds).
const baseCSP = "default-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self'; base-uri 'self'; form-action 'self'"

var cspMeta = regexp.MustCompile(`(?i)<meta http-equiv="content-security-policy" content="([^"]+)"`)

// uiHandler serves the SPA: real files directly, any other non-API path as index.html.
type uiHandler struct {
	assets fs.FS
	index  []byte
	csp    string
}

func newUIHandler(assets fs.FS) (*uiHandler, error) {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}
	return &uiHandler{assets: assets, index: index, csp: pageCSP(index)}, nil
}

// pageCSP mirrors the CSP SvelteKit wrote into the page (it contains the hashes of
// its inline bootstrap script) and adds header-only directives.
func pageCSP(index []byte) string {
	policy := baseCSP
	if m := cspMeta.FindSubmatch(index); m != nil {
		policy = string(m[1])
	}
	return policy + "; frame-ancestors 'none'"
}

func (h *uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && name != "index.html" && fs.ValidPath(name) && h.serveAsset(w, r, name) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", h.csp)
	http.ServeContent(w, r, "index.html", zeroTime, bytes.NewReader(h.index))
}

// serveAsset answers for a file in the embedded assets, and for a missing asset file (404). It
// reports false when the request should fall back to the SPA shell.
func (h *uiHandler) serveAsset(w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := h.assets.Open(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return true
	}
	if err == nil {
		stat, statErr := f.Stat()
		_ = f.Close()
		if statErr == nil && !stat.IsDir() {
			if strings.HasPrefix(name, "_app/immutable/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, h.assets, name) // #nosec G703 -- name is cleaned, fs.ValidPath-checked, and confined to the embedded FS
			return true
		}
	}
	// Missing asset files must not silently become the SPA shell.
	if path.Ext(name) != "" {
		http.NotFound(w, r)
		return true
	}
	return false
}
