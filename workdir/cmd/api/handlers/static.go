package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"time"
)

// staticFS holds the scripts and the stylesheet shared by the
// server-rendered pages, embedded from templates/static.
var staticFS = mustSub(templatesFS, "templates/static")

// stylesheet is templates/static/app.css, the Tailwind build of `make css`
// (generated at image build time, not committed), and stylesheetETag its
// content hash. stylesheet is nil when the file was not built.
var stylesheet, stylesheetETag = loadStylesheet()

func loadStylesheet() ([]byte, string) {
	b, err := fs.ReadFile(staticFS, "app.css")
	if err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(b)
	return b, `"` + hex.EncodeToString(sum[:8]) + `"`
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// FormRenderScript serves form-render.js, the field renderer shared by the
// form builder and the public form page. Pages load it with their CSP nonce.
// It is revalidated on every use so a deploy takes effect at once.
func FormRenderScript(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, staticFS, "form-render.js")
}

// Stylesheet serves app.css, the Tailwind stylesheet of every
// server-rendered page. Browsers revalidate it on every use with its ETag, so
// a deploy takes effect at once while unchanged CSS costs a 304. It 404s when
// the CSS was not built (run `make css`).
func Stylesheet(w http.ResponseWriter, r *http.Request) {
	if stylesheet == nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", stylesheetETag)
	http.ServeContent(w, r, "app.css", time.Time{}, bytes.NewReader(stylesheet))
}
