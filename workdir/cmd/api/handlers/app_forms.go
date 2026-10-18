package handlers

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
)

// formPageSize is how many submissions a page of the form page lists.
const formPageSize = 50

// payloadColumn heads the column holding payloads that are not JSON objects,
// which have no keys to spread over columns.
const payloadColumn = "payload"

// submissionMetaColumns are the metadata columns closing every CSV row.
var submissionMetaColumns = []string{"ip_address", "user_agent", "completion_time_seconds", "referer"}

// appFormPage is the data rendered by templates/form.html.
type appFormPage struct {
	appLayout
	appView
	Origin string
	Form forms.FormOverview
	// Table and JSON show the submissions of the current page, newest first.
	Table submissionTable
	JSON  string
	Pager pager
}

// pager is the position of a page of submissions within all of them.
type pager struct {
	// Page is the current page, from 1 to Pages.
	Page, Pages int
	// From and To are the 1-based positions of the first and last listed
	// submissions out of Total; both are 0 when nothing is listed.
	From, To, Total int64
	// PrevHref and NextHref link the neighbor pages; empty at either end.
	PrevHref, NextHref string
}

// newPager returns the page of size items named by the page parameter s out
// of total items, linked from base. A missing, malformed or out-of-range page
// is clamped to the nearest valid one.
func newPager(s string, size int, total int64, base string) pager {
	pages := max(1, int((total+int64(size)-1)/int64(size)))
	page, err := strconv.Atoi(s)
	if err != nil || page < 1 {
		page = 1
	}
	page = min(page, pages)
	p := pager{Page: page, Pages: pages, Total: total}
	if page > 1 {
		p.PrevHref = base + "?page=" + strconv.Itoa(page-1)
	}
	if page < pages {
		p.NextHref = base + "?page=" + strconv.Itoa(page+1)
	}
	return p
}

// offset is the number of items before the current page.
func (p pager) offset(size int) int32 {
	return int32((p.Page - 1) * size)
}

// listed records that n items starting at offset are shown.
func (p *pager) listed(offset int32, n int) {
	if n == 0 {
		return
	}
	p.From, p.To = int64(offset)+1, int64(offset)+int64(n)
}

// submissionTable lays submissions out with one column per payload key.
type submissionTable struct {
	Columns []string
	Rows    []submissionRow
}

// submissionRow is a submission with one cell per submissionTable column.
type submissionRow struct {
	Submission submissions.Submission
	Cells      []string
}

// Form renders a page of the submissions of a form of the viewer's
// workspace, selected by the page query parameter, as a table and as JSON.
func (c *appController) Form(w http.ResponseWriter, r *http.Request) {
	c.renderForm(w, r, workspaceView)
}

// AdminForm renders the submissions of any tenant's form.
func (c *appController) AdminForm(w http.ResponseWriter, r *http.Request) {
	c.renderForm(w, r, adminView)
}

func (c *appController) renderForm(w http.ResponseWriter, r *http.Request, v appView) {
	f, err := c.viewForm(r, v, "id", r.PathValue("id"))
	if err != nil {
		renderAppError(w, r, err)
		return
	}
	p := newPager(r.URL.Query().Get("page"), formPageSize, f.SubmissionCount,
		v.Base+"/forms/"+strconv.Itoa(int(f.ID)))
	offset := p.offset(formPageSize)
	subs, err := c.submissions.ListByForm(r.Context(),
		submissions.ListSubmissionsFilter{TenantID: f.TenantID, FormID: f.ID},
		submissions.Page{Offset: offset, Limit: formPageSize})
	if err != nil {
		renderAppError(w, r, err)
		return
	}
	body, err := submissionsJSON(subs)
	if err != nil {
		renderAppError(w, r, apperrors.NewInternal(err))
		return
	}
	p.listed(offset, len(subs))
	page := appFormPage{
		appLayout: appLayout{Active: v.navKey()},
		appView:   v,
		Origin:    requestOrigin(r),
		Form:      f,
		Table:     newSubmissionTable(f.Content, subs),
		JSON:      string(body),
		Pager:     p,
	}
	renderAppPage(w, r, appFormTemplate, &page)
}

// ExportJSON downloads every submission of a form of the viewer's workspace
// as a JSON array, in the shape of the GET /forms/{id}/submissions items.
func (c *appController) ExportJSON(w http.ResponseWriter, r *http.Request) {
	c.exportJSON(w, r, workspaceView)
}

// AdminExportJSON is ExportJSON for any tenant's form.
func (c *appController) AdminExportJSON(w http.ResponseWriter, r *http.Request) {
	c.exportJSON(w, r, adminView)
}

func (c *appController) exportJSON(w http.ResponseWriter, r *http.Request, v appView) {
	f, subs, err := c.allSubmissions(r, v)
	if err != nil {
		renderAppError(w, r, err)
		return
	}
	body, err := submissionsJSON(subs)
	if err != nil {
		renderAppError(w, r, apperrors.NewInternal(err))
		return
	}
	writeAttachment(w, "application/json", f.Slug+"-submissions.json", append(body, '\n'))
}

// ExportCSV downloads every submission of a form of the viewer's workspace
// as CSV, with one column per payload key followed by the metadata columns.
func (c *appController) ExportCSV(w http.ResponseWriter, r *http.Request) {
	c.exportCSV(w, r, workspaceView)
}

// AdminExportCSV is ExportCSV for any tenant's form.
func (c *appController) AdminExportCSV(w http.ResponseWriter, r *http.Request) {
	c.exportCSV(w, r, adminView)
}

func (c *appController) exportCSV(w http.ResponseWriter, r *http.Request, v appView) {
	f, subs, err := c.allSubmissions(r, v)
	if err != nil {
		renderAppError(w, r, err)
		return
	}
	body, err := submissionsCSV(newSubmissionTable(f.Content, subs))
	if err != nil {
		renderAppError(w, r, apperrors.NewInternal(err))
		return
	}
	writeAttachment(w, "text/csv; charset=utf-8", f.Slug+"-submissions.csv", body)
}

// form returns the form of the viewer's tenant whose ID is the value v of the
// parameter called name. Another tenant's form is not found.
func (c *appController) form(r *http.Request, name, v string) (forms.FormOverview, error) {
	return c.viewForm(r, workspaceView, name, v)
}

// viewForm returns the form whose ID is the value s of the parameter called
// name: any tenant's form in an admin view, else only one of the viewer's
// tenant.
func (c *appController) viewForm(r *http.Request, v appView, name, s string) (forms.FormOverview, error) {
	id, err := parseID(name, s)
	if err != nil {
		return forms.FormOverview{}, err
	}
	if v.Admin {
		return c.forms.GetAnyByID(r.Context(), id)
	}
	return c.forms.GetOverview(r.Context(), tenantFrom(r.Context()).ID, id)
}

// allSubmissions returns the form of v named by the id path value and every
// one of its submissions, newest first.
func (c *appController) allSubmissions(r *http.Request, v appView) (forms.FormOverview, []submissions.Submission, error) {
	f, err := c.viewForm(r, v, "id", r.PathValue("id"))
	if err != nil {
		return forms.FormOverview{}, nil, err
	}
	subs, err := c.submissions.ListAllByForm(r.Context(), submissions.ListSubmissionsFilter{TenantID: f.TenantID, FormID: f.ID})
	if err != nil {
		return forms.FormOverview{}, nil, err
	}
	return f, subs, nil
}

// writeAttachment sends body as a file download called filename.
func writeAttachment(w http.ResponseWriter, contentType, filename string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	h.Set("Content-Length", strconv.Itoa(len(body)))
	setAppNoStoreHeaders(h)
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// submissionsJSON encodes subs as an indented JSON array.
func submissionsJSON(subs []submissions.Submission) ([]byte, error) {
	items := make([]SubmissionResponse, len(subs))
	for i, s := range subs {
		items[i] = toSubmissionResponse(s)
	}
	return json.MarshalIndent(items, "", "  ")
}

// submissionsCSV encodes t as CSV: the submission ID and date, the ID of the
// signed-in submitter (empty for anonymous submissions), the payload columns,
// then the metadata columns.
func submissionsCSV(t submissionTable) ([]byte, error) {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	header := append([]string{"submission_id", "submitted_at", "user_id"}, t.Columns...)
	if err := cw.Write(append(header, submissionMetaColumns...)); err != nil {
		return nil, err
	}
	for _, row := range t.Rows {
		s := row.Submission
		record := make([]string, 0, len(header)+len(submissionMetaColumns))
		userID := ""
		if s.UserID != nil {
			userID = strconv.Itoa(int(*s.UserID))
		}
		record = append(record, strconv.Itoa(int(s.ID)), s.SubmittedAt.UTC().Format(time.RFC3339), userID)
		for _, cell := range row.Cells {
			record = append(record, csvSafe(cell))
		}
		record = append(record, metadataCells(s.Metadata)...)
		if err := cw.Write(record); err != nil {
			return nil, err
		}
	}
	cw.Flush()
	return buf.Bytes(), cw.Error()
}

// metadataCells renders m as the submissionMetaColumns cells.
func metadataCells(m *submissions.Metadata) []string {
	if m == nil {
		return make([]string, len(submissionMetaColumns))
	}
	seconds := ""
	if m.CompletionTimeSeconds != nil {
		seconds = strconv.Itoa(int(*m.CompletionTimeSeconds))
	}
	return []string{csvSafe(deref(m.IPAddress)), csvSafe(deref(m.UserAgent)), seconds, csvSafe(deref(m.Referer))}
}

// csvSafe defuses spreadsheet formula injection: a cell starting with a
// character that makes spreadsheets evaluate it is prefixed with a quote,
// unless it is a plain number such as -5.
func csvSafe(cell string) string {
	if cell == "" || !strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
		return cell
	}
	if _, err := strconv.ParseFloat(cell, 64); err == nil {
		return cell
	}
	return "'" + cell
}

// newSubmissionTable spreads the payload of subs over columns: first the
// field names declared by the form content, in order, then any other payload
// key, sorted. Payloads that are not JSON objects go to payloadColumn.
func newSubmissionTable(content json.RawMessage, subs []submissions.Submission) submissionTable {
	payloads := make([]map[string]json.RawMessage, len(subs))
	keys := map[string]bool{}
	hasRaw := false
	for i, s := range subs {
		if err := json.Unmarshal(s.Payload, &payloads[i]); err != nil || payloads[i] == nil {
			payloads[i] = nil
			hasRaw = true
			continue
		}
		for k := range payloads[i] {
			keys[k] = true
		}
	}

	var t submissionTable
	for _, name := range contentFieldNames(content) {
		t.Columns = append(t.Columns, name)
		delete(keys, name)
	}
	extra := make([]string, 0, len(keys))
	for k := range keys {
		extra = append(extra, k)
	}
	sort.Strings(extra)
	t.Columns = append(t.Columns, extra...)
	keyColumns := len(t.Columns)
	if hasRaw {
		t.Columns = append(t.Columns, payloadColumn)
	}

	t.Rows = make([]submissionRow, len(subs))
	for i, s := range subs {
		cells := make([]string, len(t.Columns))
		if payloads[i] == nil {
			cells[keyColumns] = cellText(s.Payload)
		} else {
			for j, col := range t.Columns[:keyColumns] {
				cells[j] = cellText(payloads[i][col])
			}
		}
		t.Rows[i] = submissionRow{Submission: s, Cells: cells}
	}
	return t
}

// contentFieldNames returns the distinct, non-empty field names of a form
// content in the form builder's shape ({"fields": [{"name": ...}]}). Content
// of any other shape declares no fields.
func contentFieldNames(content json.RawMessage) []string {
	var c struct {
		Fields []struct {
			Name string `json:"name"`
		} `json:"fields"`
	}
	if json.Unmarshal(content, &c) != nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, f := range c.Fields {
		if f.Name != "" && !seen[f.Name] {
			seen[f.Name] = true
			names = append(names, f.Name)
		}
	}
	return names
}

// cellText renders a JSON value as a table cell: strings unquoted, null or
// missing values empty, anything else as compact JSON.
func cellText(v json.RawMessage) string {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || bytes.Equal(v, []byte("null")) {
		return ""
	}
	if v[0] == '"' {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	var buf bytes.Buffer
	if json.Compact(&buf, v) != nil {
		return string(v)
	}
	return buf.String()
}

// deref returns *p, or the zero value when p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// requestOrigin returns the scheme and host of r as an absolute origin, used
// for building links and embed snippets on the /app pages.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
