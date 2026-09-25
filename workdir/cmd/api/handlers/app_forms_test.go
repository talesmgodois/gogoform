package handlers

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
)

// getAppPath requests GET path with the dashboard credentials.
func getAppPath(svc Services, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetBasicAuth(testAppConfig.Username, testAppConfig.Password)
	rec := httptest.NewRecorder()
	Routes(svc, testAppConfig).ServeHTTP(rec, req)
	return rec
}

var (
	submittedAt = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	testIP      = "203.0.113.7"
	testSeconds = int32(95)
)

// formWithSubmissions returns Services holding a form declaring the email and
// name fields, and two submissions of it.
func formWithSubmissions() Services {
	svc := testServices(forms.Form{
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

	rec := getAppPath(svc, "/app/forms/1")

	assertStatus(t, rec, http.StatusOK)
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "'nonce-") {
		t.Fatalf("Content-Security-Policy = %q", csp)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Customer survey", "/customer-survey", "Acme",
		"Showing 2 of 3", // the fake reports 3 submissions in total
		`href="/app/forms/1/submissions.json"`, `href="/app/forms/1/submissions.csv"`, `href="/app/builder?form=1"`,
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
	if fake.filter != (submissions.ListSubmissionsFilter{TenantID: 7, FormID: 1}) || fake.page != (submissions.Page{Limit: appListLimit}) {
		t.Errorf("filter = %+v, page = %+v", fake.filter, fake.page)
	}
}

func TestAppFormEmpty(t *testing.T) {
	rec := getAppPath(testServices(forms.Form{ID: 1, TenantID: 7, Title: "Empty", Slug: "empty", Content: json.RawMessage(`{}`)}), "/app/forms/1")

	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "This form has no submissions yet.") {
		t.Fatal("empty state missing")
	}
}

func TestAppFormErrors(t *testing.T) {
	for _, path := range []string{"/app/forms/9", "/app/forms/9/submissions.json", "/app/forms/9/submissions.csv", "/app/builder?form=9"} {
		assertStatus(t, getAppPath(formWithSubmissions(), path), http.StatusNotFound)
	}
	for _, path := range []string{"/app/forms/abc", "/app/forms/0/submissions.csv", "/app/builder?form=abc"} {
		assertStatus(t, getAppPath(formWithSubmissions(), path), http.StatusBadRequest)
	}
	for _, path := range []string{"/app/forms/1", "/app/forms/1/submissions.json", "/app/forms/1/submissions.csv", "/app/builder"} {
		assertStatus(t, do(t, formWithSubmissions(), http.MethodGet, path, "", false), http.StatusUnauthorized)
	}
}

func TestAppExportJSON(t *testing.T) {
	svc := formWithSubmissions()

	rec := getAppPath(svc, "/app/forms/1/submissions.json")

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
	rec := getAppPath(formWithSubmissions(), "/app/forms/1/submissions.csv")

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
