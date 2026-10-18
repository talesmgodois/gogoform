package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
)

// getApp requests GET /app with the session cookie of username (none when
// empty).
func getApp(t *testing.T, svc Services, username string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(svc, pageRequest(t, svc, "/app", username))
}

func TestAppRendersFormsAndFiles(t *testing.T) {
	desc := "Tell us <everything>"
	svc := appServices(
		forms.Form{ID: 1, TenantID: 7, Title: "Customer survey", Slug: "customer-survey", Description: &desc, IsActive: true},
		forms.Form{ID: 2, TenantID: 8, Title: "Other tenant's poll", Slug: "other-poll", IsActive: true},
	)
	svc.files.(*fakeFiles).files[testFileID] = files.File{
		ID: testFileID, TenantID: 7, Name: "logo.png", ContentType: "image/png", Size: 2048,
		Checksum: strings.Repeat("ab", 32), CreatedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
	}
	svc.files.(*fakeFiles).files["other-file"] = files.File{ID: "other-file", TenantID: 8, Name: "secret.pdf"}

	rec := getApp(t, svc, appUser)

	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	nonce := regexp.MustCompile(`'nonce-([A-Za-z0-9_=-]{24})'`).FindStringSubmatch(csp)
	if !strings.Contains(csp, "https://cdn.tailwindcss.com") || nonce == nil {
		t.Fatalf("Content-Security-Policy = %q", csp)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"https://cdn.tailwindcss.com",
		"Customer survey", "/customer-survey", "Live", "Acme",
		"Tell us &lt;everything&gt;", // escaped by html/template
		"logo.png", `href="/files/` + testFileID + `"`, "image/png", "2.0 KiB", "2026-09-24 12:00 UTC",
		// File viewer: preview trigger, download link and the nonced script.
		`data-file-preview data-file-id="` + testFileID + `" data-file-name="logo.png" data-file-type="image/png" data-file-size="2048"`,
		`href="/files/` + testFileID + `?download=1" download="logo.png"`,
		`<dialog id="file-viewer"`,
		`<script nonce="` + nonce[1] + `">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	for _, unwanted := range []string{"Other tenant's poll", "other-poll", "secret.pdf"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body shows another tenant's %q", unwanted)
		}
	}
	if svc.forms.(*fakeForms).filter.TenantID != 7 {
		t.Errorf("forms filter = %+v, want tenant 7", svc.forms.(*fakeForms).filter)
	}
	if svc.forms.(*fakeForms).page != (forms.Page{Limit: appListLimit}) {
		t.Errorf("forms page = %+v", svc.forms.(*fakeForms).page)
	}
	if svc.files.(*fakeFiles).page != (files.Page{Limit: appListLimit}) {
		t.Errorf("files page = %+v", svc.files.(*fakeFiles).page)
	}
}

func TestAppNonceIsPerRequest(t *testing.T) {
	svc := appServices()
	first := getApp(t, svc, appUser).Header().Get("Content-Security-Policy")
	second := getApp(t, svc, appUser).Header().Get("Content-Security-Policy")
	if first == second {
		t.Fatalf("Content-Security-Policy reused across requests: %q", first)
	}
}

func TestAppEmpty(t *testing.T) {
	rec := getApp(t, appServices(), appUser)

	assertStatus(t, rec, http.StatusOK)
	for _, want := range []string{"No forms have been created yet.", "No files have been uploaded yet."} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAppRequiresSignIn(t *testing.T) {
	rec := getApp(t, appServices(), "")

	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/app/signin?next=%2Fapp" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestAppIgnoresBasicCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req.SetBasicAuth(appUser, testPassword)

	rec := serve(appServices(), req)

	assertStatus(t, rec, http.StatusSeeOther)
}

func TestAppWorkspacePagesWithoutWorkspace(t *testing.T) {
	svc := appServices(forms.Form{ID: 1, TenantID: 7, Title: "Survey", Slug: "survey"})
	for _, path := range []string{"/app", "/app/forms/1", "/app/forms/1/submissions.csv", "/app/builder"} {
		rec := serve(svc, pageRequest(t, svc, path, "basic"))

		assertStatus(t, rec, http.StatusForbidden)
		if !strings.Contains(rec.Body.String(), "no workspace") {
			t.Errorf("%s: body does not explain the missing workspace", path)
		}
	}
}

func TestAppTrailingSlashRedirects(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/app/", "", false)

	assertStatus(t, rec, http.StatusMovedPermanently)
	if loc := rec.Header().Get("Location"); loc != "/app" {
		t.Fatalf("Location = %q, want /app", loc)
	}
}

func TestAppRepositoryError(t *testing.T) {
	svc := appServices()
	svc.files.(*fakeFiles).err = apperrors.NewInternal(errors.New("db down"))

	rec := getApp(t, svc, appUser)

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
