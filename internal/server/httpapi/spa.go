package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/zypher-systems/kairos/webdist"
)

type spaHandler struct {
	fs http.FileSystem
}

// newSPA builds the SPA handler; returns nil when no build is embedded.
func newSPA() http.Handler {
	dist, err := fs.Sub(webdist.FS, "dist")
	if err != nil {
		return nil
	}
	h := &spaHandler{fs: http.FS(dist)}
	// Only engage when an index.html actually exists (dev builds).
	f, err := h.fs.Open("index.html")
	if err != nil {
		return nil
	}
	f.Close()
	return h
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		h.serveFile(w, r, "index.html")
		return
	}
	upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if upath == "" || strings.HasPrefix(upath, "api/") || upath == "healthz" {
		http.NotFound(w, r)
		return
	}
	// Serve real assets; everything else falls back to index.html (SPA routes).
	f, err := h.fs.Open(upath)
	if err == nil {
		f.Close()
		h.serveFile(w, r, upath)
		return
	}
	h.serveFile(w, r, "index.html")
}

func (h *spaHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := h.fs.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if stat.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, name, stat.ModTime(), f)
}
