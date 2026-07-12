package main

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// handleStatic serves the built SPA with single-page-app fallback: an existing
// file is served directly; anything else returns index.html so the client router
// can handle the route. Caddy strips the /seqpal prefix before proxying, so the
// browser's absolute asset paths (/seqpal/assets/...) arrive here as /assets/...
func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, 405, "method not allowed")
		return
	}
	clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	fp := filepath.Join(s.cfg.webroot, filepath.FromSlash(clean))
	// Contain within webroot (defense against path traversal).
	if rel, err := filepath.Rel(s.cfg.webroot, fp); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	if fi, err := os.Stat(fp); err == nil && !fi.IsDir() {
		http.ServeFile(w, r, fp)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.cfg.webroot, "index.html"))
}
