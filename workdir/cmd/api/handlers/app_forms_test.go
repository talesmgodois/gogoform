package handlers

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
)

// getAppPath requests GET path with the session cookie of appUser.
func getAppPath(t *testing.T, svc Services, path string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(svc, pageRequest(t, svc, path, appUser))
}

var (
	submittedAt = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	testIP      = "203.0.113.7"
	testSeconds = int32(95)
)

// formWithSubmissions returns Services holding a form declaring the email and
// name fields, and two submissions of it.
func formWithSubmissions() Services {
	svc := appServices(forms.Form{
		ID: 1, TenantID: 7, Title: "Customer survey", Slug: "customer-survey", IsActive: true,
		Content: json.RawMessage(`{"version":1,"fields":[{"type":"textfield","name":"email"},{"type":"textfield","name":"name"}]}`),
	})
	svc.submissions.(*fakeSubmissions).list = []submissions.Submission{
		{
			ID: 2, FormID: 1, SubmittedAt: submittedAt, UserID: ptrTo(int32(5)),
			Payload:  json.RawMessage(`{"name":"Ada <Lovelace>","email":"ada@example.com","age":36,"tags":["a","b"],"formula":"=SUM(A1)"}`),
			Metadata: &submissions.Metadata{IPAddress: &testIP, CompletionTimeSeconds: &testSeconds},
		},
		{ID: 1, FormID: 1, SubmittedAt: submittedAt.Add(-time.Hour), Payload: json.RawMessage(`{"name":"Bob","age":null}`)},
	}
	return svc
}

func TestAppFormRendersSubmissions(t *testing.T) {
	svc := formWithSubmissions()

	rec := getAppPath(t, svc, "/app/forms/1")

	assertStatus(t, rec, http.StatusOK)
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "'nonce-") {
		t.Fatalf("Content-Security-Policy = %q", csp)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Customer survey", "/customer-survey", "Acme",
		"Showing 1–2 of 3, newest first · page 1 of 1.", // the fake reports 3 submissions in total
		`href="/app/forms/1/submissions.json"`, `href="/app/forms/1/submissions.csv"`, `href="/app/builder?form=1"`, `href="/app/builder?copy=1"`,
		"Ada &lt;Lovelace&gt;", "ada@example.com", `[&#34;a&#34;,&#34;b&#34;]`, "2026-09-24 12:00 UTC",
		`&#34;ip_address&#34;: &#34;203.0.113.7&#34;`, // JSON view
		`<script nonce="`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	// Declared fields first, in order, then the other keys sorted.
	cols := []string{">email<", ">name<", ">age<", ">formula<", ">tags<"}
	for i := 1; i < len(cols); i++ {
		if strings.Index(body, cols[i-1]) > strings.Index(body, cols[i]) {
			t.Errorf("column %s is not before %s", cols[i-1], cols[i])
		}
	}
	fake := svc.submissions.(*fakeSubmissions)
	if fake.filter != (submissions.ListSubmissionsFilter{TenantID: 7, FormID: 1}) || fake.page != (submissions.Page{Limit: formPageSize}) {
		t.Errorf("filter = %+v, page = %+v", fake.filter, fake.page)
	}
}

func TestAppFormEmpty(t *testing.T) {
	svc := appServices(forms.Form{ID: 1, TenantID: 7, Title: "Empty", Slug: "empty", Content: json.RawMessage(`{}`)})
	svc.forms.(*fakeForms).counts = map[int32]int64{1: 0}

	rec := getAppPath(t, svc, "/app/forms/1")

	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"This form has no submissions yet.", "No submissions to show.", `href="/app/forms/1/submissions.csv"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, `aria-label="Submission pages"`) {
		t.Error("a single page shows the pagination links")
	}
}

// pagedForm returns Services holding a form of tenant 7 reporting total
// submissions, and a page of two of them.
func pagedForm(total int64) Services {
	svc := formWithSubmissions()
	svc.forms.(*fakeForms).counts = map[int32]int64{1: total}
	return svc
}

func TestAppFormPagination(t *testing.T) {
	svc := pagedForm(2*formPageSize + 2)

	rec := getAppPath(t, svc, "/app/forms/1?page=2")

	assertStatus(t, rec, http.StatusOK)
	if got, want := svc.submissions.(*fakeSubmissions).page, (submissions.Page{Offset: formPageSize, Limit: formPageSize}); got != want {
		t.Errorf("page = %+v, want %+v", got, want)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Showing 51–52 of 102, newest first · page 2 of 3.", // the fake lists 2 submissions
		`href="/app/forms/1?page=1" rel="prev"`, `href="/app/forms/1?page=3" rel="next"`, "Page 2 of 3",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAppFormPaginationClampsThePage(t *testing.T) {
	tests := map[string]int32{"abc": 0, "0": 0, "-3": 0, "": 0, "99": 2 * formPageSize, "3": 2 * formPageSize, "2": formPageSize}
	for param, offset := range tests {
		svc := pagedForm(2*formPageSize + 2)

		rec := getAppPath(t, svc, "/app/forms/1?page="+param)

		assertStatus(t, rec, http.StatusOK)
		if got, want := svc.submissions.(*fakeSubmissions).page, (submissions.Page{Offset: offset, Limit: formPageSize}); got != want {
			t.Errorf("page=%q: page = %+v, want %+v", param, got, want)
		}
	}
	rec := getAppPath(t, pagedForm(2*formPageSize+2), "/app/forms/1?page=99")
	body := rec.Body.String()
	if !strings.Contains(body, "page 3 of 3") || !strings.Contains(body, `rel="prev"`) || strings.Contains(body, `rel="next"`) {
		t.Error("an out-of-range page is not clamped to the last one")
	}
}

func TestAppFormActions(t *testing.T) {
	rec := getAppPath(t, formWithSubmissions(), "/app/forms/1")

	body := rec.Body.String()
	for _, want := range []string{
		`data-form-row data-form-id="1" data-form-slug="customer-survey" data-form-title="Customer survey" data-form-active="true" data-form-deleted-href="/app"`,
		`href="/app/builder?form=1"`, "data-form-toggle", ">Deactivate</button>", "data-form-delete", "data-form-status",
		`data-copy-link="/f/customer-survey"`, `id="copy-status"`, `id="form-action-status"`, `id="form-delete-dialog"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAdminFormHasNoWriteActions(t *testing.T) {
	svc := pagedForm(2*formPageSize + 2)
	svc.forms.(*fakeForms).forms[1] = forms.Form{ID: 1, TenantID: 8, Title: "Other", Slug: "other", Content: json.RawMessage(`{}`)}

	rec := serve(svc, pageRequest(t, svc, "/app/admin/forms/1?page=2", "admin"))

	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{`href="/app/admin/forms/1/submissions.csv"`, `href="/app/admin/forms/1?page=3" rel="next"`, `data-copy-link="/f/other"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	for _, unwanted := range []string{"data-form-row", "data-form-toggle", "data-form-delete", `id="form-delete-dialog"`, "/app/builder"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body contains %q", unwanted)
		}
	}
}

func TestAppFormOfAnotherTenantIsNotFound(t *testing.T) {
	svc := formWithSubmissions()
	svc.forms.(*fakeForms).forms[2] = forms.Form{ID: 2, TenantID: 8, Title: "Other", Slug: "other"}
	for _, path := range []string{"/app/forms/2", "/app/forms/2/submissions.json", "/app/forms/2/submissions.csv", "/app/builder?form=2"} {
		rec := getAppPath(t, svc, path)

		assertStatus(t, rec, http.StatusNotFound)
		if strings.Contains(rec.Body.String(), "Other") {
			t.Errorf("%s: body shows the other tenant's form", path)
		}
	}
	// The form lookup fails before any submission is read.
	if got := svc.submissions.(*fakeSubmissions).filter; got != (submissions.ListSubmissionsFilter{}) {
		t.Errorf("submissions of another tenant's form were listed: %+v", got)
	}
}

func TestAppFormErrors(t *testing.T) {
	for _, path := range []string{"/app/forms/9", "/app/forms/9/submissions.json", "/app/forms/9/submissions.csv", "/app/builder?form=9"} {
		assertMessagePage(t, getAppPath(t, formWithSubmissions(), path), http.StatusNotFound, "Form not found")
	}
	for _, path := range []string{"/app/forms/abc", "/app/forms/0/submissions.csv", "/app/forms/x/submissions.json", "/app/builder?form=abc"} {
		assertMessagePage(t, getAppPath(t, formWithSubmissions(), path), http.StatusBadRequest, "Invalid form")
	}
	for _, path := range []string{"/app/forms/1", "/app/forms/1/submissions.json", "/app/forms/1/submissions.csv", "/app/builder"} {
		assertStatus(t, do(t, formWithSubmissions(), http.MethodGet, path, "", false), http.StatusSeeOther)
	}
}

func TestAppFormRepositoryError(t *testing.T) {
	for _, path := range []string{"/app/forms/1", "/app/forms/1/submissions.json", "/app/forms/1/submissions.csv"} {
		svc := formWithSubmissions()
		svc.submissions.(*fakeSubmissions).listErr = apperrors.NewInternal(errors.New("db down"))

		rec := getAppPath(t, svc, path)

		assertMessagePage(t, rec, http.StatusInternalServerError, "Something went wrong")
		if strings.Contains(rec.Body.String(), "db down") {
			t.Errorf("%s: internal error details leaked to the client", path)
		}
	}
}

// assertMessagePage checks that rec is the HTML message page titled title,
// with the given status and a link back to the dashboard.
func assertMessagePage(t *testing.T, rec *httptest.ResponseRecorder, status int, title string) {
	t.Helper()
	assertStatus(t, rec, status)
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want an HTML page", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "" {
		t.Errorf("Content-Disposition = %q, want no download", cd)
	}
	body := rec.Body.String()
	for _, want := range []string{"<h1 class=\"text-2xl font-bold tracking-tight\">" + title + "</h1>", `href="/app"`, "Back to your forms"} {
		if !strings.Contains(body, want) {
			t.Errorf("message page does not contain %q: %s", want, body)
		}
	}
}

func TestAppExportJSON(t *testing.T) {
	svc := formWithSubmissions()

	rec := getAppPath(t, svc, "/app/forms/1/submissions.json")

	assertStatus(t, rec, http.StatusOK)
	assertAttachment(t, rec, "application/json", "customer-survey-submissions.json")
	got := decode[[]SubmissionResponse](t, rec)
	if len(got) != 2 || got[0].ID != 2 || got[0].Metadata == nil || *got[0].Metadata.IPAddress != testIP || got[1].Metadata != nil {
		t.Fatalf("submissions = %+v", got)
	}
	if svc.submissions.(*fakeSubmissions).filter != (submissions.ListSubmissionsFilter{TenantID: 7, FormID: 1}) {
		t.Errorf("filter = %+v", svc.submissions.(*fakeSubmissions).filter)
	}
}

func TestAppExportCSV(t *testing.T) {
	rec := getAppPath(t, formWithSubmissions(), "/app/forms/1/submissions.csv")

	assertStatus(t, rec, http.StatusOK)
	assertAttachment(t, rec, "text/csv; charset=utf-8", "customer-survey-submissions.csv")
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	want := [][]string{
		{"submission_id", "submitted_at", "user_id", "email", "name", "age", "formula", "tags", "ip_address", "user_agent", "completion_time_seconds", "referer"},
		{"2", "2026-09-24T12:00:00Z", "5", "ada@example.com", "Ada <Lovelace>", "36", "'=SUM(A1)", `["a","b"]`, testIP, "", "95", ""},
		{"1", "2026-09-24T11:00:00Z", "", "", "Bob", "", "", "", "", "", "", ""},
	}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("CSV =\n%q\nwant\n%q", records, want)
	}
}

// assertAttachment fails unless rec is a download of the given type and name.
func assertAttachment(t *testing.T, rec *httptest.ResponseRecorder, contentType, filename string) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != contentType {
		t.Errorf("Content-Type = %q, want %q", got, contentType)
	}
	if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename=`+filename; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestNewSubmissionTableNonObjectPayloads(t *testing.T) {
	subs := []submissions.Submission{
		{ID: 3, Payload: json.RawMessage(`{"payload":"key named like the column","a":1}`)},
		{ID: 2, Payload: json.RawMessage(`[1, 2]`)},
		{ID: 1, Payload: json.RawMessage(`"just text"`)},
	}

	got := newSubmissionTable(json.RawMessage(`not the builder shape`), subs)

	if want := []string{"a", "payload", "payload"}; !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("columns = %q, want %q", got.Columns, want)
	}
	want := [][]string{{"1", "key named like the column", ""}, {"", "", "[1,2]"}, {"", "", "just text"}}
	for i, row := range got.Rows {
		if !reflect.DeepEqual(row.Cells, want[i]) {
			t.Errorf("row %d cells = %q, want %q", i, row.Cells, want[i])
		}
	}
}

func TestContentFieldNames(t *testing.T) {
	tests := map[string][]string{
		`{"fields":[{"name":"b"},{"name":""},{"name":"a"},{"name":"b"}]}`: {"b", "a"},
		`{"fields":"nope"}`: nil,
		`[]`:                nil,
		`{}`:                nil,
	}
	for content, want := range tests {
		if got := contentFieldNames(json.RawMessage(content)); !reflect.DeepEqual(got, want) {
			t.Errorf("contentFieldNames(%s) = %q, want %q", content, got, want)
		}
	}
}

func TestCellText(t *testing.T) {
	tests := map[string]string{
		``: "", `null`: "", `"a \"b\""`: `a "b"`, `true`: "true", `1.50`: "1.50",
		`{ "x" : [1, 2] }`: `{"x":[1,2]}`,
	}
	for in, want := range tests {
		if got := cellText(json.RawMessage(in)); got != want {
			t.Errorf("cellText(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestCSVSafe(t *testing.T) {
	tests := map[string]string{
		"": "", "plain": "plain", "-5": "-5", "+1.5": "+1.5",
		"=1+1": "'=1+1", "+cmd": "'+cmd", "-2+3": "'-2+3", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx",
	}
	for in, want := range tests {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func ptrTo[T any](v T) *T { return &v }
