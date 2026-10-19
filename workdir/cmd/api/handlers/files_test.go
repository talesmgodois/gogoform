package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"app/internal/pkg/files"
	"app/internal/pkg/forms"
)

const testFileID = "0b6a3b2e-8f1c-4c4e-9d5e-2f1a7c3b9e10"

// upload posts a multipart body with one part per field (name, filename,
// content type, content) to /files, authenticated with testAPIKey.
func upload(t *testing.T, svc Services, parts ...[4]string) *httptest.ResponseRecorder {
	t.Helper()
	req := multipartRequest(t, "/files", parts...)
	req.Header.Set(apiKeyHeader, testAPIKey)
	return serve(svc, req)
}

// multipartRequest builds a POST to path with a multipart body holding one
// part per field (name, filename, content type, content).
func multipartRequest(t *testing.T, path string, parts ...[4]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		disposition := `form-data; name="` + p[0] + `"`
		if p[1] != "" {
			disposition += `; filename="` + p[1] + `"`
		}
		h.Set("Content-Disposition", disposition)
		if p[2] != "" {
			h.Set("Content-Type", p[2])
		}
		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(p[3]))
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestUploadFile(t *testing.T) {
	svc := testServices()
	rec := upload(t, svc,
		[4]string{"note", "", "", "ignored"},
		[4]string{"file", "../../logo.png", "image/png", "\x89PNG data"},
	)
	assertStatus(t, rec, http.StatusCreated)

	in := svc.files.(*fakeFiles).created
	if in.TenantID != testTenant.ID || in.Name != "logo.png" || in.ContentType != "image/png" || string(in.Data) != "\x89PNG data" {
		t.Fatalf("input = %+v", in)
	}
	body := decode[FileResponse](t, rec)
	if body.ID != testFileID || body.URL != "/files/"+testFileID || body.Size != 9 {
		t.Fatalf("body = %+v", body)
	}
	if loc := rec.Header().Get("Location"); loc != body.URL {
		t.Fatalf("Location = %q", loc)
	}
}

func TestUploadFileSniffsContentType(t *testing.T) {
	svc := testServices()
	assertStatus(t, upload(t, svc, [4]string{"file", "a.bin", "application/octet-stream", "%PDF-1.7 ..."}), http.StatusCreated)
	if ct := svc.files.(*fakeFiles).created.ContentType; ct != "application/pdf" {
		t.Fatalf("content type = %q", ct)
	}
}

func TestUploadFileInvalid(t *testing.T) {
	svc := testServices()
	assertStatus(t, upload(t, svc, [4]string{"other", "a.txt", "", "x"}), http.StatusBadRequest)
	assertStatus(t, upload(t, svc, [4]string{"file", "", "", "not a file"}), http.StatusBadRequest)
	assertStatus(t, upload(t, svc, [4]string{"file", "empty.txt", "", ""}), http.StatusBadRequest)
	assertStatus(t, upload(t, svc, [4]string{"file", "big.bin", "", strings.Repeat("x", maxUploadBytes+1)}), http.StatusBadRequest)
	assertStatus(t, do(t, svc, http.MethodPost, "/files", `{"file":"x"}`, true), http.StatusBadRequest)
	assertStatus(t, do(t, svc, http.MethodPost, "/files", "", false), http.StatusUnauthorized)
}

func TestGetFile(t *testing.T) {
	svc := testServices()
	svc.files.(*fakeFiles).files[testFileID] = files.File{
		ID: testFileID, TenantID: 99, Name: "résumé.html", ContentType: "text/html", Checksum: "abc", Data: []byte("<script>x</script>"),
	}

	// Files are public: no API key, and any tenant's file can be read.
	rec := do(t, svc, http.MethodGet, "/files/"+testFileID, "", false)
	assertStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "<script>x</script>" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	for header, want := range map[string]string{
		"Content-Type":            "text/html",
		"Content-Disposition":     "inline; filename*=utf-8''r%C3%A9sum%C3%A9.html",
		"ETag":                    `"abc"`,
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/files/"+testFileID, nil)
	req.Header.Set("Range", "bytes=0-7")
	rec = httptest.NewRecorder()
	Routes(svc, testFeatures, testStripe).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusPartialContent)
	if rec.Body.String() != "<script>" {
		t.Fatalf("range body = %q", rec.Body.String())
	}

	assertStatus(t, do(t, svc, http.MethodGet, "/files/unknown", "", false), http.StatusNotFound)
}

func TestGetFileDownload(t *testing.T) {
	svc := testServices()
	svc.files.(*fakeFiles).files[testFileID] = files.File{ID: testFileID, Name: "logo.png", ContentType: "image/png", Data: []byte("png")}

	tests := map[string]string{
		"?download=1":     "attachment; filename=logo.png",
		"?download=true":  "attachment; filename=logo.png",
		"?download=0":     "inline; filename=logo.png",
		"?download=maybe": "inline; filename=logo.png",
	}
	for query, want := range tests {
		rec := do(t, svc, http.MethodGet, "/files/"+testFileID+query, "", false)
		assertStatus(t, rec, http.StatusOK)
		if got := rec.Header().Get("Content-Disposition"); got != want {
			t.Errorf("%s: Content-Disposition = %q, want %q", query, got, want)
		}
	}
}

func TestDeleteFile(t *testing.T) {
	svc := testServices()
	fake := svc.files.(*fakeFiles)
	fake.files[testFileID] = files.File{ID: testFileID, TenantID: testTenant.ID}
	fake.files["other"] = files.File{ID: "other", TenantID: 99}

	assertStatus(t, do(t, svc, http.MethodDelete, "/files/"+testFileID, "", false), http.StatusUnauthorized)
	assertStatus(t, do(t, svc, http.MethodDelete, "/files/"+testFileID, "", true), http.StatusNoContent)
	if _, ok := fake.files[testFileID]; ok {
		t.Fatal("file was not deleted")
	}
	assertStatus(t, do(t, svc, http.MethodDelete, "/files/other", "", true), http.StatusNotFound)
}

func TestSanitizeFileName(t *testing.T) {
	for in, want := range map[string]string{
		" report.pdf ":           "report.pdf",
		"a\x00b\nc.txt":          "abc.txt",
		"\x01":                   "file",
		strings.Repeat("é", 300): strings.Repeat("é", maxFileTextLen),
	} {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// uploadForm is a public form, owned by another tenant than testTenant, with
// file fields (one in a panel) and a non-file field.
var uploadForm = forms.Form{
	ID: 20, TenantID: 99, Title: "Jobs", Slug: "jobs", IsActive: true, PublicAvailable: true, AcceptAnonymous: true,
	Content: []byte(`{"fields":[
		{"type":"textfield","name":"email"},
		{"type":"file","name":"photo","accept":"image/*,.pdf","max_size_mb":0.001},
		{"type":"panel","title":"Docs","fields":[{"type":"file","name":"cv"}]}
	]}`),
}

// publicUpload posts file to the form slug's upload endpoint for the field
// named field, without credentials.
func publicUpload(t *testing.T, svc Services, slug, field string, file [4]string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(svc, multipartRequest(t, "/public/forms/"+slug+"/files", [4]string{"field", "", "", field}, file))
}

func TestPublicUploadFile(t *testing.T) {
	svc := testServices(uploadForm)
	rec := publicUpload(t, svc, "jobs", "photo", [4]string{"file", "me.png", "image/png", "\x89PNG"})
	assertStatus(t, rec, http.StatusCreated)
	if in := svc.files.(*fakeFiles).created; in.TenantID != uploadForm.TenantID || in.Name != "me.png" || in.ContentType != "image/png" {
		t.Fatalf("input = %+v", in)
	}
	if body := decode[FileResponse](t, rec); body.ID != testFileID || rec.Header().Get("Location") != body.URL {
		t.Fatalf("body = %+v, Location %q", body, rec.Header().Get("Location"))
	}

	// Extensions match too, and fields nested in panels are found.
	assertStatus(t, publicUpload(t, svc, "jobs", "photo", [4]string{"file", "cv.PDF", "application/octet-stream", "x"}), http.StatusCreated)
	assertStatus(t, publicUpload(t, svc, "jobs", "cv", [4]string{"file", "cv.txt", "text/plain", "x"}), http.StatusCreated)
}

func TestPublicUploadFileRejects(t *testing.T) {
	svc := testServices(uploadForm)
	png := [4]string{"file", "me.png", "image/png", "x"}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"too large for the field": publicUpload(t, svc, "jobs", "photo", [4]string{"file", "big.png", "image/png", strings.Repeat("x", 1100)}),
		"too large for the API":   publicUpload(t, svc, "jobs", "cv", [4]string{"file", "big.bin", "", strings.Repeat("x", maxUploadBytes+1)}),
		"wrong type":              publicUpload(t, svc, "jobs", "photo", [4]string{"file", "notes.txt", "text/plain", "x"}),
		"unknown field":           publicUpload(t, svc, "jobs", "nope", png),
		"non-file field":          publicUpload(t, svc, "jobs", "email", png),
		"layout component":        publicUpload(t, svc, "jobs", "Docs", png),
		"missing field":           serve(svc, multipartRequest(t, "/public/forms/jobs/files", png)),
		"field after the file":    serve(svc, multipartRequest(t, "/public/forms/jobs/files", png, [4]string{"field", "", "", "photo"})),
		"missing file":            serve(svc, multipartRequest(t, "/public/forms/jobs/files", [4]string{"field", "", "", "photo"})),
	} {
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", name, rec.Code, rec.Body.String())
		}
	}
	if in := svc.files.(*fakeFiles).created; in.Data != nil {
		t.Fatalf("stored %+v", in)
	}
}

func TestPublicUploadFileAvailability(t *testing.T) {
	inactive := uploadForm
	inactive.ID, inactive.Slug, inactive.IsActive = 21, "closed", false
	private := uploadForm
	private.ID, private.Slug, private.PublicAvailable = 22, "private", false
	identified := uploadForm
	identified.ID, identified.Slug, identified.AcceptAnonymous = 23, "identified", false
	svc := testServices(uploadForm, inactive, private, identified)
	png := [4]string{"file", "me.png", "image/png", "x"}

	assertStatus(t, publicUpload(t, svc, "closed", "photo", png), http.StatusForbidden)
	assertStatus(t, publicUpload(t, svc, "missing", "photo", png), http.StatusNotFound)
	assertStatus(t, publicUpload(t, svc, "private", "photo", png), http.StatusUnauthorized)
	assertStatus(t, publicUpload(t, svc, "identified", "photo", png), http.StatusUnauthorized)

	// A signed-in user can upload to both.
	token := bearer(tokenFor(t, svc, "basic"))
	for _, slug := range []string{"private", "identified"} {
		req := multipartRequest(t, "/public/forms/"+slug+"/files", [4]string{"field", "", "", "photo"}, png)
		req.Header.Set("Authorization", token)
		assertStatus(t, serve(svc, req), http.StatusCreated)
	}
}
