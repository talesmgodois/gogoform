package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"app/internal/config"
	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/plans"
	"app/internal/pkg/submissions"
	"app/internal/pkg/tenants"
	"app/internal/pkg/webhooks"
)

const testAPIKey = "test-key"

// testFeatures enables the development-only routes, as in development.
var testFeatures = featureFlags(true)

// testStripe leaves Stripe unconfigured, so the billing routes are not mounted.
var testStripe config.StripeConfig

// featureFlags returns a FeaturesConfig with every flag set to enabled.
func featureFlags(enabled bool) config.FeaturesConfig {
	return config.FeaturesConfig{PublicTenantSignup: &enabled, Samples: &enabled}
}

// testTenant is the tenant owning testAPIKey.
var testTenant = tenants.Tenant{ID: 7, Name: "Acme", APIKey: testAPIKey, IsActive: true}

// appUser is the user of newTestAuth whose workspace is testTenant in the
// Services of appServices.
const appUser = "creator"

// fakeTenants is a tenants.Repository knowing testTenant and the tenants of
// owned, keyed by owner user ID. Rotated API keys are kept in keys, keyed by
// tenant ID, and replace the original ones; owned tenants are updated in
// place by the billing methods.
type fakeTenants struct {
	owned   map[int32]tenants.Tenant
	keys    map[int32]string
	created tenants.CreateTenantInput
	err     error
	// ownerErr, when set, is returned by GetByOwner.
	ownerErr error
	// subscriptions records every UpdateSubscription call, in order.
	subscriptions []tenants.SubscriptionUpdate
}

func (f *fakeTenants) Create(_ context.Context, in tenants.CreateTenantInput) (tenants.Tenant, error) {
	f.created = in
	if f.err != nil {
		return tenants.Tenant{}, f.err
	}
	return tenants.Tenant{ID: 1, Name: in.Name, APIKey: in.APIKey, IsActive: true}, nil
}

// all returns every known tenant with its current API key.
func (f *fakeTenants) all() []tenants.Tenant {
	out := []tenants.Tenant{testTenant}
	for _, t := range f.owned {
		out = append(out, t)
	}
	for i, t := range out {
		if key, ok := f.keys[t.ID]; ok {
			out[i].APIKey = key
		}
	}
	return out
}

func (f *fakeTenants) find(match func(tenants.Tenant) bool) (tenants.Tenant, error) {
	for _, t := range f.all() {
		if match(t) {
			return t, nil
		}
	}
	return tenants.Tenant{}, apperrors.NewNotFound("tenant not found")
}

func (f *fakeTenants) GetByAPIKey(_ context.Context, key string) (tenants.Tenant, error) {
	return f.find(func(t tenants.Tenant) bool { return t.APIKey == key })
}

func (f *fakeTenants) GetByID(_ context.Context, id int32) (tenants.Tenant, error) {
	return f.find(func(t tenants.Tenant) bool { return t.ID == id })
}

func (f *fakeTenants) GetByOwner(_ context.Context, userID int32) (tenants.Tenant, error) {
	if f.ownerErr != nil {
		return tenants.Tenant{}, f.ownerErr
	}
	return f.find(func(t tenants.Tenant) bool { return t.OwnerUserID != nil && *t.OwnerUserID == userID })
}

func (f *fakeTenants) RotateAPIKey(ctx context.Context, tenantID int32, newKey string) (tenants.Tenant, error) {
	if f.err != nil {
		return tenants.Tenant{}, f.err
	}
	if _, err := f.GetByID(ctx, tenantID); err != nil {
		return tenants.Tenant{}, err
	}
	if f.keys == nil {
		f.keys = map[int32]string{}
	}
	f.keys[tenantID] = newKey
	return f.GetByID(ctx, tenantID)
}

func (f *fakeTenants) GetByStripeCustomerID(_ context.Context, customerID string) (tenants.Tenant, error) {
	return f.find(func(t tenants.Tenant) bool { return customerID != "" && t.StripeCustomerID == customerID })
}

// updateOwned applies change to the owned tenant matching match and returns
// it; testTenant cannot be changed.
func (f *fakeTenants) updateOwned(match func(tenants.Tenant) bool, change func(*tenants.Tenant)) (tenants.Tenant, error) {
	if f.err != nil {
		return tenants.Tenant{}, f.err
	}
	for owner, t := range f.owned {
		if match(t) {
			change(&t)
			f.owned[owner] = t
			return f.find(func(c tenants.Tenant) bool { return c.ID == t.ID })
		}
	}
	return tenants.Tenant{}, apperrors.NewNotFound("tenant not found")
}

func (f *fakeTenants) SetStripeCustomer(_ context.Context, tenantID int32, customerID string) (tenants.Tenant, error) {
	return f.updateOwned(
		func(t tenants.Tenant) bool { return t.ID == tenantID },
		func(t *tenants.Tenant) { t.StripeCustomerID = customerID },
	)
}

func (f *fakeTenants) LinkSubscription(_ context.Context, customerID, subscriptionID string) (tenants.Tenant, error) {
	return f.updateOwned(
		func(t tenants.Tenant) bool { return customerID != "" && t.StripeCustomerID == customerID },
		func(t *tenants.Tenant) {
			if t.StripeSubscriptionID != subscriptionID {
				t.SubscriptionStatus, t.CurrentPeriodEnd = "", nil
			}
			t.StripeSubscriptionID = subscriptionID
		},
	)
}

func (f *fakeTenants) UpdateSubscription(_ context.Context, in tenants.SubscriptionUpdate) (tenants.Tenant, error) {
	f.subscriptions = append(f.subscriptions, in)
	return f.updateOwned(
		func(t tenants.Tenant) bool { return in.CustomerID != "" && t.StripeCustomerID == in.CustomerID },
		func(t *tenants.Tenant) {
			t.StripeSubscriptionID = in.SubscriptionID
			t.SubscriptionStatus = in.Status
			t.CurrentPeriodEnd = in.CurrentPeriodEnd
			t.Plan = in.Plan
		},
	)
}

// fakeForms is a forms.Repository holding forms in a map keyed by ID.
type fakeForms struct {
	forms   map[int32]forms.Form
	created forms.CreateFormInput
	updated forms.UpdateFormInput
	filter  forms.ListFormsFilter
	page    forms.Page
	err     error
	// counts overrides the submission count of a form, 3 by default.
	counts map[int32]int64
	// tenantInactive marks tenants whose forms GetBySlug reports as belonging
	// to an inactive tenant.
	tenantInactive map[int32]bool
	// tenantPlans sets the plan GetBySlug reports for a tenant, free by default.
	tenantPlans map[int32]tenants.Plan
}

// plan returns the plan reported for the tenant id.
func (f *fakeForms) plan(tenantID int32) tenants.Plan {
	if p, ok := f.tenantPlans[tenantID]; ok {
		return p
	}
	return tenants.PlanFree
}

// count returns the submission count reported for the form id.
func (f *fakeForms) count(id int32) int64 {
	if n, ok := f.counts[id]; ok {
		return n
	}
	return 3
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

// GetBySlug returns the form whatever its state; tenantInactive marks the
// tenants reported as inactive and tenantPlans sets their plans.
func (f *fakeForms) GetBySlug(_ context.Context, slug string) (forms.FormBySlug, error) {
	for _, form := range f.forms {
		if form.Slug == slug {
			return forms.FormBySlug{Form: form, TenantName: "Acme", TenantActive: !f.tenantInactive[form.TenantID], TenantPlan: f.plan(form.TenantID)}, nil
		}
	}
	return forms.FormBySlug{}, apperrors.NewNotFound("form not found")
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
	return forms.FormOverview{FormSummary: forms.FormSummary{Form: form, SubmissionCount: f.count(id)}, TenantName: "Acme"}, nil
}

func (f *fakeForms) GetOverview(_ context.Context, tenantID, id int32) (forms.FormOverview, error) {
	form, ok := f.forms[id]
	if !ok || form.TenantID != tenantID {
		return forms.FormOverview{}, apperrors.NewNotFound("form not found")
	}
	return forms.FormOverview{FormSummary: forms.FormSummary{Form: form, SubmissionCount: f.count(id)}, TenantName: "Acme"}, nil
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
	// listErr, when set, is returned by ListByForm and ListAllByForm.
	listErr error
	// monthCount is returned by CountByTenantSince.
	monthCount int64
	// monthErr, when set, is returned by CountByTenantSince.
	monthErr error
}

func (f *fakeSubmissions) Create(_ context.Context, in submissions.CreateSubmissionInput) (submissions.Submission, error) {
	f.created = in
	return submissions.Submission{ID: 11, FormID: in.FormID, Payload: in.Payload, UserID: in.UserID}, nil
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
	return f.list, f.listErr
}

func (f *fakeSubmissions) ListAllByForm(_ context.Context, filter submissions.ListSubmissionsFilter) ([]submissions.Submission, error) {
	f.filter = filter
	return f.list, f.listErr
}

func (f *fakeSubmissions) CountByTenantSince(context.Context, int32, time.Time) (int64, error) {
	return f.monthCount, f.monthErr
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

func (f *fakeFiles) GetInfoByID(ctx context.Context, id string) (files.File, error) {
	file, err := f.GetByID(ctx, id)
	file.Data = nil
	return file, err
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

func (f *fakeFiles) ListByTenant(ctx context.Context, tenantID int32, page files.Page) ([]files.FileSummary, error) {
	all, err := f.ListAll(ctx, page)
	var out []files.FileSummary
	for _, file := range all {
		if file.TenantID == tenantID {
			out = append(out, file)
		}
	}
	return out, err
}

func (f *fakeFiles) CountByTenant(ctx context.Context, tenantID int32) (int64, error) {
	var n int64
	for _, file := range f.files {
		if file.TenantID == tenantID {
			n++
		}
	}
	return n, nil
}

func (f *fakeFiles) StorageUsedByTenant(_ context.Context, tenantID int32) (int64, error) {
	var n int64
	for _, file := range f.files {
		if file.TenantID == tenantID {
			n += file.Size
		}
	}
	return n, nil
}

func (f *fakeFiles) Delete(ctx context.Context, tenantID int32, id string) error {
	if file, ok := f.files[id]; !ok || file.TenantID != tenantID {
		return apperrors.NewNotFound("file not found")
	}
	delete(f.files, id)
	return nil
}

// testPassword is the password of every user created by newTestAuth.
const testPassword = "correct horse"

// fakeUsers is an in-memory auth.Repository.
type fakeUsers struct {
	users []auth.StoredUser
}

func (f *fakeUsers) Create(_ context.Context, in auth.CreateUserInput) (auth.User, error) {
	for _, u := range f.users {
		if in.Email != "" && u.Email == in.Email {
			return auth.User{}, apperrors.NewConflict("email already registered")
		}
		if u.Username == in.Username {
			return auth.User{}, apperrors.NewConflict("username already taken")
		}
	}
	u := auth.StoredUser{User: auth.User{ID: int32(len(f.users) + 1), Username: in.Username, Email: in.Email, Role: in.Role, IsActive: true}, PasswordHash: in.PasswordHash}
	f.users = append(f.users, u)
	return u.User, nil
}

func (f *fakeUsers) CreateIfNotExists(ctx context.Context, in auth.CreateUserInput) (bool, error) {
	_, err := f.Create(ctx, in)
	return err == nil, nil
}

func (f *fakeUsers) GetByUsername(_ context.Context, username string) (auth.StoredUser, error) {
	for _, u := range f.users {
		if u.Username == username {
			return u, nil
		}
	}
	return auth.StoredUser{}, apperrors.NewNotFound("user not found")
}

func (f *fakeUsers) GetByEmail(_ context.Context, email string) (auth.StoredUser, error) {
	for _, u := range f.users {
		if u.Email != "" && u.Email == email {
			return u, nil
		}
	}
	return auth.StoredUser{}, apperrors.NewNotFound("user not found")
}

func (f *fakeUsers) GetByID(_ context.Context, id int32) (auth.User, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u.User, nil
		}
	}
	return auth.User{}, apperrors.NewNotFound("user not found")
}

func (f *fakeUsers) List(context.Context, auth.Page) ([]auth.User, error) {
	out := make([]auth.User, len(f.users))
	for i, u := range f.users {
		out[i] = u.User
	}
	return out, nil
}

func (f *fakeUsers) UpdateRole(_ context.Context, id int32, role auth.Role) (auth.User, error) {
	for i := range f.users {
		if f.users[i].ID == id {
			f.users[i].Role = role
			return f.users[i].User, nil
		}
	}
	return auth.User{}, apperrors.NewNotFound("user not found")
}

// newTestAuth returns an auth.Service over a fakeUsers holding one user per
// role: "admin" (ID 1), "creator" (ID 2) and "basic" (ID 3), all with
// testPassword.
func newTestAuth() *auth.Service {
	users := &fakeUsers{}
	svc, err := auth.NewService(users, auth.Options{
		Secret:       []byte(strings.Repeat("k", auth.MinSecretLen)),
		PasswordCost: bcrypt.MinCost,
		Tx:           &database.FakeTxRunner{},
		UsersFor:     func(db.Querier) auth.Repository { return users },
		TenantsFor:   func(db.Querier) tenants.Repository { return &fakeTenants{} },
	})
	if err != nil {
		panic(err)
	}
	for _, u := range []struct {
		name string
		role auth.Role
	}{{"admin", auth.RoleAdmin}, {"creator", auth.RoleFormCreator}, {"basic", auth.RoleBasic}} {
		if _, err := svc.EnsureUser(context.Background(), u.name, "", testPassword, u.role); err != nil {
			panic(err)
		}
	}
	return svc
}

// tokenFor signs in as username and returns the access token.
func tokenFor(t *testing.T, svc Services, username string) string {
	t.Helper()
	tok, err := svc.auth.SignIn(context.Background(), username, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	return tok.AccessToken
}

// serve sends req through Routes(svc, testFeatures, testStripe).
func serve(svc Services, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	Routes(svc, testFeatures, testStripe).ServeHTTP(rec, req)
	return rec
}

// testServices returns Services backed by fakes, with forms holding fs and
// the users of newTestAuth.
func testServices(fs ...forms.Form) Services {
	fakeF, fakeS, fakeFl := newFakeForms(fs...), &fakeSubmissions{}, &fakeFiles{files: map[string]files.File{}}
	return Services{
		auth:        newTestAuth(),
		tenants:     &fakeTenants{},
		forms:       fakeF,
		submissions: fakeS,
		webhooks:    &fakeWebhooks{},
		files:       fakeFl,
		plans:       plans.NewEnforcer(fakeF, fakeS, fakeFl),
	}
}

// appServices is testServices where appUser owns testTenant, so the /app
// pages of appUser show the forms and files of tenant 7.
func appServices(fs ...forms.Form) Services {
	svc := testServices(fs...)
	owned := testTenant
	owned.OwnerUserID = ptrTo(int32(2))
	svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{2: owned}}
	return svc
}

// do sends a request with the given body (if any) through Routes(svc, testFeatures, testStripe),
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
	Routes(svc, testFeatures, testStripe).ServeHTTP(rec, req)
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
