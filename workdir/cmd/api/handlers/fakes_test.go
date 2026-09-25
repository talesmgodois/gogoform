package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/config"
	apperrors "app/internal/errors"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
	"app/internal/pkg/tenants"
	"app/internal/pkg/webhooks"
)

const testAPIKey = "test-key"

// testAppConfig enables the /app dashboard with known credentials.
var testAppConfig = config.AppConfig{Username: "admin", Password: "s3cret"}

// testTenant is the tenant owning testAPIKey.
var testTenant = tenants.Tenant{ID: 7, Name: "Acme", APIKey: testAPIKey, IsActive: true}

// fakeTenants is a tenants.Repository knowing only testTenant.
type fakeTenants struct {
	created tenants.CreateTenantInput
	err     error
}

func (f *fakeTenants) Create(_ context.Context, in tenants.CreateTenantInput) (tenants.Tenant, error) {
	f.created = in
	if f.err != nil {
		return tenants.Tenant{}, f.err
	}
	return tenants.Tenant{ID: 1, Name: in.Name, APIKey: in.APIKey, IsActive: true}, nil
}

func (f *fakeTenants) GetByAPIKey(_ context.Context, key string) (tenants.Tenant, error) {
	if key != testAPIKey {
		return tenants.Tenant{}, apperrors.NewNotFound("tenant not found")
	}
	return testTenant, nil
}

// fakeForms is a forms.Repository holding forms in a map keyed by ID.
type fakeForms struct {
	forms   map[int32]forms.Form
	created forms.CreateFormInput
	updated forms.UpdateFormInput
	filter  forms.ListFormsFilter
	page    forms.Page
	err     error
}

func newFakeForms(fs ...forms.Form) *fakeForms {
	f := &fakeForms{forms: map[int32]forms.Form{}}
	for _, form := range fs {
		f.forms[form.ID] = form
	}
	return f
}

func (f *fakeForms) Create(_ context.Context, in forms.CreateFormInput) (forms.Form, error) {
	f.created = in
	if f.err != nil {
		return forms.Form{}, f.err
	}
	return forms.Form{ID: 1, TenantID: in.TenantID, Title: in.Title, Slug: in.Slug, IsActive: true, Content: in.Content}, nil
}

func (f *fakeForms) GetByID(_ context.Context, tenantID, id int32) (forms.FormDetails, error) {
	form, ok := f.forms[id]
	if !ok || form.TenantID != tenantID {
		return forms.FormDetails{}, apperrors.NewNotFound("form not found")
	}
	return forms.FormDetails{Form: form, TenantName: "Acme"}, nil
}

func (f *fakeForms) GetPublicBySlug(_ context.Context, slug string) (forms.Form, error) {
	for _, form := range f.forms {
		if form.Slug == slug && form.IsActive {
			return form, nil
		}
	}
	return forms.Form{}, apperrors.NewNotFound("form not found")
}

func (f *fakeForms) List(_ context.Context, filter forms.ListFormsFilter, page forms.Page) ([]forms.FormSummary, error) {
	f.filter, f.page = filter, page
	var out []forms.FormSummary
	for _, form := range f.forms {
		if form.TenantID == filter.TenantID {
			out = append(out, forms.FormSummary{Form: form, SubmissionCount: 3})
		}
	}
	return out, nil
}

func (f *fakeForms) Count(_ context.Context, filter forms.ListFormsFilter) (int64, error) {
	items, _ := f.List(context.Background(), filter, f.page)
	return int64(len(items)), nil
}

func (f *fakeForms) ListAll(_ context.Context, page forms.Page) ([]forms.FormOverview, error) {
	f.page = page
	if f.err != nil {
		return nil, f.err
	}
	var out []forms.FormOverview
	for _, form := range f.forms {
		out = append(out, forms.FormOverview{FormSummary: forms.FormSummary{Form: form, SubmissionCount: 3}, TenantName: "Acme"})
	}
	return out, nil
}

func (f *fakeForms) CountAll(context.Context) (int64, error) {
	return int64(len(f.forms)), nil
}

func (f *fakeForms) GetAnyByID(_ context.Context, id int32) (forms.FormOverview, error) {
	form, ok := f.forms[id]
	if !ok {
		return forms.FormOverview{}, apperrors.NewNotFound("form not found")
	}
	return forms.FormOverview{FormSummary: forms.FormSummary{Form: form, SubmissionCount: 3}, TenantName: "Acme"}, nil
}

func (f *fakeForms) Update(_ context.Context, in forms.UpdateFormInput) (forms.Form, error) {
	f.updated = in
	if _, err := f.GetByID(context.Background(), in.TenantID, in.ID); err != nil {
		return forms.Form{}, err
	}
	return forms.Form{ID: in.ID, TenantID: in.TenantID, Title: in.Title, Slug: in.Slug, IsActive: in.IsActive, Content: in.Content}, nil
}

func (f *fakeForms) Delete(ctx context.Context, tenantID, id int32) error {
	if _, err := f.GetByID(ctx, tenantID, id); err != nil {
		return err
	}
	delete(f.forms, id)
	return nil
}

// fakeSubmissions is a submissions.Repository recording its inputs.
type fakeSubmissions struct {
	created     submissions.CreateSubmissionInput
	metadata    submissions.Metadata
	metadataErr error
	filter      submissions.ListSubmissionsFilter
	page        submissions.Page
	list        []submissions.Submission
}

func (f *fakeSubmissions) Create(_ context.Context, in submissions.CreateSubmissionInput) (submissions.Submission, error) {
	f.created = in
	return submissions.Submission{ID: 11, FormID: in.FormID, Payload: in.Payload}, nil
}

func (f *fakeSubmissions) AddMetadata(_ context.Context, _ int32, m submissions.Metadata) (submissions.Metadata, error) {
	f.metadata = m
	if f.metadataErr != nil {
		return submissions.Metadata{}, f.metadataErr
	}
	return m, nil
}

func (f *fakeSubmissions) ListByForm(_ context.Context, filter submissions.ListSubmissionsFilter, page submissions.Page) ([]submissions.Submission, error) {
	f.filter, f.page = filter, page
	return f.list, nil
}

func (f *fakeSubmissions) ListAllByForm(_ context.Context, filter submissions.ListSubmissionsFilter) ([]submissions.Submission, error) {
	f.filter = filter
	return f.list, nil
}

// fakeWebhooks is a webhooks.Repository recording its inputs.
type fakeWebhooks struct {
	created webhooks.CreateWebhookInput
	list    []webhooks.Webhook
}

func (f *fakeWebhooks) Create(_ context.Context, in webhooks.CreateWebhookInput) (webhooks.Webhook, error) {
	f.created = in
	return webhooks.Webhook{ID: 5, FormID: in.FormID, TargetURL: in.TargetURL, SecretToken: in.SecretToken, IsActive: true}, nil
}

func (f *fakeWebhooks) ListActiveByForm(_ context.Context, _ int32) ([]webhooks.Webhook, error) {
	return f.list, nil
}

// fakeFiles is a files.Repository holding files in a map keyed by ID.
type fakeFiles struct {
	files   map[string]files.File
	created files.CreateFileInput
	page    files.Page
	err     error
}

func (f *fakeFiles) Create(_ context.Context, in files.CreateFileInput) (files.File, error) {
	f.created = in
	return files.File{ID: testFileID, TenantID: in.TenantID, Name: in.Name, ContentType: in.ContentType, Size: int64(len(in.Data))}, nil
}

func (f *fakeFiles) GetByID(_ context.Context, id string) (files.File, error) {
	file, ok := f.files[id]
	if !ok {
		return files.File{}, apperrors.NewNotFound("file not found")
	}
	return file, nil
}

func (f *fakeFiles) ListAll(_ context.Context, page files.Page) ([]files.FileSummary, error) {
	f.page = page
	if f.err != nil {
		return nil, f.err
	}
	var out []files.FileSummary
	for _, file := range f.files {
		file.Data = nil
		out = append(out, files.FileSummary{File: file, TenantName: "Acme"})
	}
	return out, nil
}

func (f *fakeFiles) CountAll(context.Context) (int64, error) {
	return int64(len(f.files)), nil
}

func (f *fakeFiles) Delete(ctx context.Context, tenantID int32, id string) error {
	if file, ok := f.files[id]; !ok || file.TenantID != tenantID {
		return apperrors.NewNotFound("file not found")
	}
	delete(f.files, id)
	return nil
}

// testServices returns Services backed by fakes, with forms holding fs.
func testServices(fs ...forms.Form) Services {
	return Services{
		tenants:     &fakeTenants{},
		forms:       newFakeForms(fs...),
		submissions: &fakeSubmissions{},
		webhooks:    &fakeWebhooks{},
		files:       &fakeFiles{files: map[string]files.File{}},
	}
}

// do sends a request with the given body (if any) through Routes(svc, testAppConfig),
// authenticated with testAPIKey when auth is true.
func do(t *testing.T, svc Services, method, path, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if auth {
		req.Header.Set(apiKeyHeader, testAPIKey)
	}
	rec := httptest.NewRecorder()
	Routes(svc, testAppConfig).ServeHTTP(rec, req)
	return rec
}

// decode unmarshals the recorded body into a value of type T.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

// assertStatus fails the test when rec does not have the wanted status.
func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, want, rec.Body.String())
	}
}
