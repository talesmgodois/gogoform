package handlers

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setStylesheet replaces the embedded stylesheet for the duration of the
// test, so it does not depend on `make css` having run.
func setStylesheet(t *testing.T, css []byte, etag string) {
	t.Helper()
	prevCSS, prevETag := stylesheet, stylesheetETag
	stylesheet, stylesheetETag = css, etag
	t.Cleanup(func() { stylesheet, stylesheetETag = prevCSS, prevETag })
}

func TestStylesheet(t *testing.T) {
	setStylesheet(t, []byte("body{margin:0}"), `"abc"`)

	rec := httptest.NewRecorder()
	Stylesheet(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if rec.Header().Get("ETag") != `"abc"` || rec.Header().Get("Cache-Control") != "no-cache" || rec.Body.String() != "body{margin:0}" {
		t.Fatalf("headers %v, body %q", rec.Header(), rec.Body)
	}

	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	req.Header.Set("If-None-Match", `"abc"`)
	rec = httptest.NewRecorder()
	Stylesheet(rec, req)
	assertStatus(t, rec, http.StatusNotModified)
}

func TestStylesheetNotBuilt(t *testing.T) {
	setStylesheet(t, nil, "")
	rec := httptest.NewRecorder()
	Stylesheet(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	assertStatus(t, rec, http.StatusNotFound)
}

// TestTemplatesHaveNoInlineStyles guards the page CSPs (style-src 'self'),
// which block style attributes and <style> tags, and the removal of the
// Tailwind Play CDN.
func TestTemplatesHaveNoInlineStyles(t *testing.T) {
	err := fs.WalkDir(templatesFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		b, err := fs.ReadFile(templatesFS, path)
		if err != nil {
			return err
		}
		for _, banned := range []string{` style="`, `<style`, "cdn.tailwindcss.com"} {
			if strings.Contains(string(b), banned) {
				t.Errorf("%s contains %q", path, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
