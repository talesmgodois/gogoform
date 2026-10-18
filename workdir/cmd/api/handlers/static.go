package handlers

import (
	"io/fs"
	"net/http"
)

// staticFS holds the scripts shared by the server-rendered pages, embedded
// from templates/static.
var staticFS = mustSub(templatesFS, "templates/static")

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
