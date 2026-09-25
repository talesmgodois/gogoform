package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"app/internal/pkg/files"
)

const testFileID = "0b6a3b2e-8f1c-4c4e-9d5e-2f1a7c3b9e10"

// upload posts a multipart body with one part per field (name, filename,
// content type, content) to /files, authenticated with testAPIKey.
func upload(t *testing.T, svc services, parts ...[4]string) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(http.MethodPost, "/files", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(apiKeyHeader, testAPIKey)
	rec := httptest.NewRecorder()
	routes(svc, testAppConfig).ServeHTTP(rec, req)
	return rec
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
	routes(svc, testAppConfig).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusPartialContent)
	if rec.Body.String() != "<script>" {
		t.Fatalf("range body = %q", rec.Body.String())
	}

	assertStatus(t, do(t, svc, http.MethodGet, "/files/unknown", "", false), http.StatusNotFound)
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
