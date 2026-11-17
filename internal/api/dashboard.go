package api

import (
	"bytes"
	"errors"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

const (
	indexFile    = "index.html"
	apiBaseMeta  = "labelgate-api-base"
	cacheHashed  = "public, max-age=31536000, immutable"
	cacheRevalid = "no-cache"
)

// dashboardHandler serves the embedded SPA below /dashboard/.
//
//   - Existing files are served as-is; files under assets/ are content-hashed
//     and cached immutably.
//   - Missing files with an extension (e.g. a stale assets/index-abc.js) are a
//     real 404, never index.html.
//   - Any other path is a client-side route and gets index.html, with the API
//     base path injected as <meta name="labelgate-api-base">.
type dashboardHandler struct {
	assets fs.FS
	index  []byte // index.html with the meta tag; nil if the dashboard is not built
	log    *slog.Logger
}

func newDashboardHandler(assets fs.FS, apiBase string, log *slog.Logger) *dashboardHandler {
	h := &dashboardHandler{assets: assets, log: log}
	raw, err := fs.ReadFile(assets, indexFile)
	if err != nil {
		log.Warn("dashboard index.html not available; dashboard disabled", "error", err)
		return h
	}
	h.index = injectAPIBase(raw, apiBase)
	return h
}

// injectAPIBase inserts the meta tag before </head> (or at the top if the
// document has no head).
func injectAPIBase(doc []byte, apiBase string) []byte {
	tag := []byte(`<meta name="` + apiBaseMeta + `" content="` + html.EscapeString(apiBase) + `" />`)
	if i := bytes.Index(bytes.ToLower(doc), []byte("</head>")); i >= 0 {
		out := make([]byte, 0, len(doc)+len(tag)+1)
		out = append(out, doc[:i]...)
		out = append(out, tag...)
		out = append(out, '\n')
		return append(out, doc[i:]...)
	}
	return append(append(tag, '\n'), doc...)
}

func (h *dashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setDashboardHeaders(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, dashboardPrefix+"/")), "/")
	if rel == "" || rel == indexFile {
		h.serveIndex(w, r)
		return
	}
	if h.serveFile(w, r, rel) {
		return
	}
	if path.Ext(rel) != "" {
		http.NotFound(w, r)
		return
	}
	h.serveIndex(w, r)
}

// serveFile serves an existing regular file and reports whether it did.
func (h *dashboardHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	info, err := fs.Stat(h.assets, name)
	if err != nil || info.IsDir() {
		return false
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheHashed)
	} else {
		w.Header().Set("Cache-Control", cacheRevalid)
	}
	if ct := contentTypeFor(name); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeFileFS(w, r, h.assets, name)
	return true
}

func (h *dashboardHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if h.index == nil {
		http.Error(w, "dashboard not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cacheRevalid)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(h.index); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		h.log.Debug("dashboard write failed", "error", err)
	}
}

// contentTypeFor covers types Go's built-in table lacks; "" lets net/http decide.
func contentTypeFor(name string) string {
	switch path.Ext(name) {
	case ".woff2":
		return "font/woff2"
	case ".svg":
		return "image/svg+xml"
	}
	return ""
}
