package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"app/internal/pkg/forms"
	"app/internal/pkg/tenants"
)

// publicSurvey is a public form of testTenant to render on /f.
func publicSurvey() forms.Form {
	desc := "Tell us <what> you think"
	return forms.Form{
		ID: 5, TenantID: testTenant.ID, Title: "Feedback & ideas", Slug: "feedback", Description: &desc, IsActive: true,
		Content:         json.RawMessage(`{"version":1,"fields":[{"type":"textfield","name":"email","label":"Email","input_type":"email","required":true}]}`),
		PublicAvailable: true, AcceptAnonymous: true,
	}
}

func TestPublicFormPage(t *testing.T) {
	svc := testServices(publicSurvey())
	rec := serve(svc, pageRequest(t, svc, "/f/feedback", ""))
	assertStatus(t, rec, http.StatusOK)

	body := rec.Body.String()
	for _, want := range []string{
		`<title>Feedback &amp; ideas</title>`,
		`Tell us &lt;what&gt; you think`,
		`data-slug="feedback"`,
		`data-content="{&#34;version&#34;:1,&#34;fields&#34;:[{&#34;type&#34;:&#34;textfield&#34;,&#34;name&#34;:&#34;email&#34;`,
		`<script nonce="`, `src="/static/form-render.js"`, `FormRender.renderContent(content, PREFIX)`,
		`"/public/forms/" + encodeURIComponent(slug)`, `endpoint + "/submissions"`, `credentials: "same-origin"`,
		`location.assign(page + "/thanks" + (embed ? "?embed=1" : ""))`, `showFieldErrors(err.fields)`,
		"Powered by gogoform",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, "Signed in as") {
		t.Error("anonymous page shows a signed-in user")
	}

	h := rec.Header()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'nonce-", "style-src 'self';", "connect-src 'self'", "form-action 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q does not contain %q", csp, want)
		}
	}
	if got := h.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := h.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestPublicFormPageShowsSignedInUser(t *testing.T) {
	svc := testServices(publicSurvey())
	rec := serve(svc, pageRequest(t, svc, "/f/feedback", "basic"))
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `Signed in as <span class="font-medium text-slate-300">basic</span>`) {
		t.Errorf("body does not show the signed-in user")
	}
}

func TestPublicFormPageStates(t *testing.T) {
	past := time.Date(2020, time.March, 4, 15, 30, 0, 0, time.UTC)
	future := time.Now().AddDate(1, 0, 0).UTC().Truncate(time.Minute)
	survey := func(edit func(*forms.Form)) forms.Form {
		f := publicSurvey()
		edit(&f)
		return f
	}
	tests := []struct {
		name, path, user string
		form             forms.Form
		tenantInactive   bool
		want             int
		contains         []string
	}{
		{name: "available", path: "/f/feedback", form: publicSurvey(), want: http.StatusOK,
			contains: []string{`id="public-form"`}},
		{name: "not found", path: "/f/missing", form: publicSurvey(), want: http.StatusNotFound,
			contains: []string{"Form not found"}},
		{name: "inactive", path: "/f/feedback", form: survey(func(f *forms.Form) { f.IsActive = false }), want: http.StatusForbidden,
			contains: []string{"Not accepting responses", "Feedback &amp; ideas"}},
		{name: "tenant inactive", path: "/f/feedback", form: publicSurvey(), tenantInactive: true, want: http.StatusForbidden,
			contains: []string{"Not accepting responses"}},
		{name: "not started", path: "/f/feedback", form: survey(func(f *forms.Form) { f.StartDate = &future }), want: http.StatusForbidden,
			contains: []string{"Not open yet", "This form opens on " + future.Format(publicDateLayout)}},
		{name: "expired", path: "/f/feedback", form: survey(func(f *forms.Form) { f.EndDate = &past }), want: http.StatusForbidden,
			contains: []string{"Form closed", "This form closed on March 4, 2020 at 15:30 UTC"}},
		{name: "private anonymous", path: "/f/feedback?ref=mail", form: survey(func(f *forms.Form) { f.PublicAvailable = false }), want: http.StatusUnauthorized,
			contains: []string{"Sign in required", `href="/app/signin?next=%2Ff%2Ffeedback%3Fref%3Dmail" class`}},
		// An embedded form signs in on the top-level window, back to the regular page.
		{name: "private anonymous embed", path: "/f/feedback?embed=1", form: survey(func(f *forms.Form) { f.PublicAvailable = false }), want: http.StatusUnauthorized,
			contains: []string{"Sign in required", `href="/app/signin?next=%2Ff%2Ffeedback" target="_top"`, "Powered by gogoform"}},
		{name: "expired embed", path: "/f/feedback?embed=1", form: survey(func(f *forms.Form) { f.EndDate = &past }), want: http.StatusForbidden,
			contains: []string{"Form closed", `<body class="bg-transparent`, "Powered by gogoform"}},
		{name: "not found embed", path: "/f/missing?embed=1", form: publicSurvey(), want: http.StatusNotFound,
			contains: []string{"Form not found", `<body class="bg-transparent`}},
		{name: "private signed in", path: "/f/feedback", user: "basic", form: survey(func(f *forms.Form) { f.PublicAvailable = false }), want: http.StatusOK,
			contains: []string{`id="public-form"`, "Signed in as"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := testServices(tt.form)
			if tt.tenantInactive {
				svc.forms.(*fakeForms).tenantInactive = map[int32]bool{testTenant.ID: true}
			}
			rec := serve(svc, pageRequest(t, svc, tt.path, tt.user))
			assertStatus(t, rec, tt.want)
			body := rec.Body.String()
			for _, want := range tt.contains {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q", want)
				}
			}
			if tt.want != http.StatusOK && strings.Contains(body, `id="public-form"`) {
				t.Error("message page renders the form")
			}
			if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			assertFrameAncestors(t, rec, strings.Contains(tt.path, "embed=1"))
		})
	}
}

func TestPublicFormPageSubmissionLimit(t *testing.T) {
	tests := []struct {
		name       string
		plan       tenants.Plan
		monthCount int64
		want       int
	}{
		{"free below limit", tenants.PlanFree, 99, http.StatusOK},
		{"free at limit", tenants.PlanFree, 100, http.StatusForbidden},
		{"pro at free limit", tenants.PlanPro, 100, http.StatusOK},
		{"pro below limit", tenants.PlanPro, 9_999, http.StatusOK},
		{"pro at limit", tenants.PlanPro, 10_000, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := testServices(publicSurvey())
			svc.forms.(*fakeForms).tenantPlans = map[int32]tenants.Plan{testTenant.ID: tt.plan}
			svc.submissions.(*fakeSubmissions).monthCount = tt.monthCount

			rec := serve(svc, pageRequest(t, svc, "/f/feedback", ""))
			assertStatus(t, rec, tt.want)
			body := rec.Body.String()
			if tt.want == http.StatusOK {
				if !strings.Contains(body, `id="public-form"`) {
					t.Error("body does not render the form")
				}
				return
			}
			for _, want := range []string{"Not accepting responses", "This form is not accepting responses right now.", "Feedback &amp; ideas"} {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q", want)
				}
			}
			for _, leak := range []string{`id="public-form"`, "plan", "Upgrade", "limit"} {
				if strings.Contains(body, leak) {
					t.Errorf("body contains %q", leak)
				}
			}
		})
	}
}

func TestPublicPagesPoweredByBadge(t *testing.T) {
	inactive := publicSurvey()
	inactive.IsActive = false
	tests := []struct {
		name, path string
		form       forms.Form
		plan       tenants.Plan
		want       int
		badge      bool
	}{
		{"free form", "/f/feedback", publicSurvey(), tenants.PlanFree, http.StatusOK, true},
		{"pro form", "/f/feedback", publicSurvey(), tenants.PlanPro, http.StatusOK, false},
		{"free embed", "/f/feedback?embed=1", publicSurvey(), tenants.PlanFree, http.StatusOK, true},
		{"pro embed", "/f/feedback?embed=1", publicSurvey(), tenants.PlanPro, http.StatusOK, false},
		{"free message", "/f/feedback", inactive, tenants.PlanFree, http.StatusForbidden, true},
		{"pro message", "/f/feedback", inactive, tenants.PlanPro, http.StatusForbidden, false},
		{"free thanks", "/f/feedback/thanks", publicSurvey(), tenants.PlanFree, http.StatusOK, true},
		{"pro thanks", "/f/feedback/thanks", publicSurvey(), tenants.PlanPro, http.StatusOK, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := testServices(tt.form)
			svc.forms.(*fakeForms).tenantPlans = map[int32]tenants.Plan{testTenant.ID: tt.plan}

			rec := serve(svc, pageRequest(t, svc, tt.path, ""))
			assertStatus(t, rec, tt.want)
			if got := strings.Contains(rec.Body.String(), "Powered by gogoform"); got != tt.badge {
				t.Errorf("badge shown = %v, want %v", got, tt.badge)
			}
		})
	}
}

func TestPublicFormPageEmbedMode(t *testing.T) {
	svc := testServices(publicSurvey())
	for _, tt := range []struct {
		path  string
		embed bool
	}{
		{"/f/feedback", false},
		{"/f/feedback?embed=0", false},
		{"/f/feedback?embed=1", true},
	} {
		t.Run(tt.path, func(t *testing.T) {
			rec := serve(svc, pageRequest(t, svc, tt.path, ""))
			assertStatus(t, rec, http.StatusOK)
			assertFrameAncestors(t, rec, tt.embed)
			body := rec.Body.String()
			for _, want := range []string{`id="public-form"`, "Powered by gogoform"} {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q", want)
				}
			}
			if got := strings.Contains(body, `<body class="bg-transparent`); got != tt.embed {
				t.Errorf("minimal embed layout = %v, want %v", got, tt.embed)
			}
			if got := strings.Contains(body, "bg-slate-950"); got == tt.embed {
				t.Errorf("page background = %v, want %v", got, !tt.embed)
			}
		})
	}
}

// assertFrameAncestors checks that rec lets any site frame it when embed is
// set, and no site otherwise, with no X-Frame-Options overriding the policy.
func assertFrameAncestors(t *testing.T, rec *httptest.ResponseRecorder, embed bool) {
	t.Helper()
	csp := rec.Header().Get("Content-Security-Policy")
	want, notWant := "frame-ancestors 'none'", "frame-ancestors *"
	if embed {
		want, notWant = notWant, want
	}
	if !strings.Contains(csp, want) || strings.Contains(csp, notWant) {
		t.Errorf("CSP %q does not contain %q only", csp, want)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options = %q, want none", got)
	}
}

func TestPublicThanksPage(t *testing.T) {
	inactive := publicSurvey()
	inactive.IsActive = false
	tests := []struct {
		name, path string
		form       forms.Form
		want       int
		contains   []string
	}{
		{name: "thanks", path: "/f/feedback/thanks", form: publicSurvey(), want: http.StatusOK,
			contains: []string{"<title>Thank you!</title>", "Feedback &amp; ideas", "Your response has been recorded.", `href="/f/feedback"`, "Powered by gogoform"}},
		{name: "embed", path: "/f/feedback/thanks?embed=1", form: publicSurvey(), want: http.StatusOK,
			contains: []string{"Thank you!", `href="/f/feedback?embed=1"`}},
		// The thank-you page follows a submission, which may close the form.
		{name: "inactive form", path: "/f/feedback/thanks", form: inactive, want: http.StatusOK,
			contains: []string{"Thank you!"}},
		{name: "not found", path: "/f/missing/thanks", form: publicSurvey(), want: http.StatusNotFound,
			contains: []string{"Form not found"}},
		{name: "not found embed", path: "/f/missing/thanks?embed=1", form: publicSurvey(), want: http.StatusNotFound,
			contains: []string{"Form not found", `<body class="bg-transparent`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := testServices(tt.form)
			rec := serve(svc, pageRequest(t, svc, tt.path, ""))
			assertStatus(t, rec, tt.want)
			body := rec.Body.String()
			for _, want := range tt.contains {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q", want)
				}
			}
			if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			assertFrameAncestors(t, rec, strings.Contains(tt.path, "embed=1"))
		})
	}
}

func TestPublicSubmissionWithSessionCookie(t *testing.T) {
	svc := testServices(identifiedForm)
	post := func(user, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/public/forms/leave/submissions", strings.NewReader(`{"payload":{}}`))
		if user != "" {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tokenFor(t, svc, user)})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		return serve(svc, req)
	}

	assertStatus(t, post("", ""), http.StatusUnauthorized)
	// A cross-origin request does not get the user of the cookie.
	assertStatus(t, post("creator", "https://evil.example"), http.StatusUnauthorized)

	rec := post("creator", "http://example.com")
	assertStatus(t, rec, http.StatusCreated)
	if body := decode[SubmissionResponse](t, rec); body.UserID == nil || *body.UserID != 2 {
		t.Fatalf("body = %+v", body)
	}
}
