package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"app/internal/config"
	apperrors "app/internal/errors"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
)

// getApp requests GET /app with the given basic credentials (none when user
// is empty) through routes(svc, cfg).
func getApp(svc services, cfg config.AppConfig, user, pass string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	routes(svc, cfg).ServeHTTP(rec, req)
	return rec
}

func TestAppRendersFormsAndFiles(t *testing.T) {
	desc := "Tell us <everything>"
	svc := testServices(forms.Form{ID: 1, TenantID: 7, Title: "Customer survey", Slug: "customer-survey", Description: &desc, IsActive: true})
	svc.files.(*fakeFiles).files[testFileID] = files.File{
		ID: testFileID, TenantID: 7, Name: "logo.png", ContentType: "image/png", Size: 2048,
		Checksum: strings.Repeat("ab", 32), CreatedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
	}

	rec := getApp(svc, testAppConfig, "admin", "s3cret")

	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "https://cdn.tailwindcss.com") {
		t.Fatalf("Content-Security-Policy = %q", csp)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"https://cdn.tailwindcss.com",
		"Customer survey", "/customer-survey", "Live", "Acme",
		"Tell us &lt;everything&gt;", // escaped by html/template
		"logo.png", `href="/files/` + testFileID + `"`, "image/png", "2.0 KiB", "2026-09-24 12:00 UTC",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if svc.forms.(*fakeForms).page != (forms.Page{Limit: appListLimit}) {
		t.Errorf("forms page = %+v", svc.forms.(*fakeForms).page)
	}
	if svc.files.(*fakeFiles).page != (files.Page{Limit: appListLimit}) {
		t.Errorf("files page = %+v", svc.files.(*fakeFiles).page)
	}
}

func TestAppEmpty(t *testing.T) {
	rec := getApp(testServices(), testAppConfig, "admin", "s3cret")

	assertStatus(t, rec, http.StatusOK)
	for _, want := range []string{"No forms have been created yet.", "No files have been uploaded yet."} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAppRequiresCredentials(t *testing.T) {
	tests := []struct {
		name, user, pass string
	}{
		{"none", "", ""},
		{"wrong user", "root", "s3cret"},
		{"wrong password", "admin", "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := getApp(testServices(), testAppConfig, tt.user, tt.pass)

			assertStatus(t, rec, http.StatusUnauthorized)
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
				t.Fatalf("WWW-Authenticate = %q", got)
			}
		})
	}
}

func TestAppDisabledWithoutCredentials(t *testing.T) {
	rec := getApp(testServices(), config.AppConfig{}, "", "")

	assertStatus(t, rec, http.StatusNotFound)
}

func TestAppTrailingSlashRedirects(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/app/", "", false)

	assertStatus(t, rec, http.StatusMovedPermanently)
	if loc := rec.Header().Get("Location"); loc != "/app" {
		t.Fatalf("Location = %q, want /app", loc)
	}
}

func TestAppRepositoryError(t *testing.T) {
	svc := testServices()
	svc.files.(*fakeFiles).err = apperrors.NewInternal(errors.New("db down"))

	rec := getApp(svc, testAppConfig, "admin", "s3cret")

	assertStatus(t, rec, http.StatusInternalServerError)
	if strings.Contains(rec.Body.String(), "db down") {
		t.Fatal("internal error details leaked to the client")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 10 << 20: "10.0 MiB", 3 << 30: "3.0 GiB"}
	for n, want := range tests {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormStatus(t *testing.T) {
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	tests := []struct {
		name string
		form forms.Form
		want string
	}{
		{"inactive", forms.Form{IsActive: false}, "Inactive"},
		{"scheduled", forms.Form{IsActive: true, StartDate: &future}, "Scheduled"},
		{"ended", forms.Form{IsActive: true, EndDate: &past}, "Ended"},
		{"live", forms.Form{IsActive: true, StartDate: &past, EndDate: &future}, "Live"},
	}
	for _, tt := range tests {
		if got := formStatus(tt.form).Label; got != tt.want {
			t.Errorf("%s: status = %q, want %q", tt.name, got, tt.want)
		}
	}
}
