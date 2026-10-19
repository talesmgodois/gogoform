package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/forms"
	"app/internal/pkg/plans"
)

// publicContentSecurityPolicy is the policy of the public form pages. Like
// appContentSecurityPolicy it allows the Tailwind Play CDN and the scripts
// carrying the per-request nonce (the first %s verb); the page script only
// calls the public form endpoints of this origin. The second %s verb is the
// frame-ancestors source list: publicFrameAncestors or embedFrameAncestors.
const publicContentSecurityPolicy = "default-src 'none'; script-src https://cdn.tailwindcss.com 'nonce-%s'; " +
	"style-src 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors %s"

const (
	// publicFrameAncestors forbids framing the regular public pages.
	publicFrameAncestors = "'none'"
	// embedFrameAncestors lets any site frame the embed mode pages (embed=1).
	embedFrameAncestors = "*"
)

var (
	publicFormTemplate    = parsePublicPage("public_form.html")
	publicMessageTemplate = parsePublicPage("public_message.html")
)

// publicDateLayout formats the start and end dates on the message pages.
const publicDateLayout = "January 2, 2006 at 15:04 MST"

// parsePublicPage parses the public page template called name along with the
// powered-by partial.
func parsePublicPage(name string) *template.Template {
	return template.Must(template.New(name).ParseFS(templatesFS, "templates/powered_by.html", "templates/"+name))
}

// publicLayout is the data shared by the public form pages.
type publicLayout struct {
	// Nonce authorizes the page's scripts in the Content-Security-Policy.
	Nonce string
	// PoweredBy shows the "Powered by gogoform" badge (see showPoweredBy).
	PoweredBy bool
	// Embed renders the minimal layout of a page framed by another site and
	// lets any site frame it (see isEmbed).
	Embed bool
}

// isEmbed reports whether r asks for the embed mode of a public page, the
// embed=1 query parameter of the iframe snippet.
func isEmbed(r *http.Request) bool {
	return r.URL.Query().Get("embed") == "1"
}

// publicFormPage is the data rendered by templates/public_form.html.
type publicFormPage struct {
	publicLayout
	Title, Slug string
	Description *string
	// Content is the form definition JSON, rendered by form-render.js.
	Content string
	// User is the signed-in respondent; nil when anonymous.
	User *auth.User
}

// publicMessagePage is the data rendered by templates/public_message.html,
// shown instead of a form that cannot be filled in.
type publicMessagePage struct {
	publicLayout
	// FormTitle names the form the message is about; empty when unknown.
	FormTitle      string
	Title, Message string
	// Href and Link are an optional action, such as signing in.
	Href, Link string
	// TopLevel opens Href in the top-level window rather than in the frame
	// of an embedded page.
	TopLevel bool
}

// publicPageController serves the pages respondents fill forms in on.
type publicPageController struct {
	forms forms.Repository
	auth  *auth.Service
	plans *plans.Enforcer
}

// Form renders the form of the slug path value for respondents. The page
// submits the answers to POST /public/forms/{slug}/submissions with the
// session cookie of the optional signed-in user. A form that cannot be
// filled in renders a message page instead: not found (404), not accepting
// responses (also when its tenant reached the plan's monthly submission
// limit), not open yet or closed (403), or private with no session (401,
// with a sign-in link back to the form). With embed=1 every page renders the
// embed mode, which other sites may frame.
func (c *publicPageController) Form(w http.ResponseWriter, r *http.Request) {
	embed := isEmbed(r)
	f, err := c.forms.GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		c.writeFormError(w, r, embed, err)
		return
	}
	layout := publicLayout{PoweredBy: showPoweredBy(f.TenantPlan), Embed: embed}
	msg := unavailableMessage(f.Form, f.TenantActive, time.Now())
	if msg == nil {
		err := checkSubmissionLimit(r, c.plans, f)
		switch {
		case apperrors.HTTPStatus(err) == http.StatusPaymentRequired:
			msg = notAcceptingMessage(f.Form)
		case err != nil:
			apperrors.WriteHTTP(w, r, err)
			return
		}
	}
	if msg != nil {
		msg.publicLayout = layout
		renderPublicPage(w, r, http.StatusForbidden, publicMessageTemplate, msg)
		return
	}
	u := sessionUserOf(w, r, c.auth)
	if !f.PublicAvailable && u == nil {
		// The /app pages cannot be framed and a framing site gets no
		// SameSite=Lax session cookie, so an embedded form signs in on the
		// top-level window and comes back to the regular form page.
		next := r.URL.RequestURI()
		if embed {
			next = "/f/" + url.PathEscape(f.Slug)
		}
		renderPublicPage(w, r, http.StatusUnauthorized, publicMessageTemplate, &publicMessagePage{
			publicLayout: layout,
			FormTitle:    f.Title,
			Title:        "Sign in required",
			Message:      "This form is private. Sign in to fill it in.",
			Href:         "/app/signin?next=" + url.QueryEscape(next),
			Link:         "Sign in",
			TopLevel:     embed,
		})
		return
	}
	renderPublicPage(w, r, http.StatusOK, publicFormTemplate, &publicFormPage{
		publicLayout: layout,
		Title:        f.Title,
		Slug:         f.Slug,
		Description:  f.Description,
		Content:      string(f.Content),
		User:         u,
	})
}

// Thanks renders the thank-you page the /f page redirects to once a
// submission is created, with a link to fill the form in again. It keeps
// embed=1 on that link so an embedded form stays embedded. Unknown slugs
// render the not-found page.
func (c *publicPageController) Thanks(w http.ResponseWriter, r *http.Request) {
	embed := isEmbed(r)
	f, err := c.forms.GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		c.writeFormError(w, r, embed, err)
		return
	}
	again := "/f/" + url.PathEscape(f.Slug)
	if embed {
		again += "?embed=1"
	}
	renderPublicPage(w, r, http.StatusOK, publicMessageTemplate, &publicMessagePage{
		publicLayout: publicLayout{PoweredBy: showPoweredBy(f.TenantPlan), Embed: embed},
		FormTitle:    f.Title,
		Title:        "Thank you!",
		Message:      "Your response has been recorded.",
		Href:         again,
		Link:         "Submit another response",
	})
}

// writeFormError writes err, the error of looking a form up by slug: the
// not-found page, in embed mode when embed is set, when there is no such
// form, an error response otherwise.
func (c *publicPageController) writeFormError(w http.ResponseWriter, r *http.Request, embed bool, err error) {
	if apperrors.HTTPStatus(err) == http.StatusNotFound {
		renderPublicPage(w, r, http.StatusNotFound, publicMessageTemplate, &publicMessagePage{
			publicLayout: publicLayout{Embed: embed},
			Title:        "Form not found",
			Message:      "This form does not exist or has been removed. Check the link you were given.",
		})
		return
	}
	apperrors.WriteHTTP(w, r, err)
}

// unavailableMessage returns the message page explaining why f accepts no
// responses at now, or nil when it is available.
func unavailableMessage(f forms.Form, tenantActive bool, now time.Time) *publicMessagePage {
	msg := &publicMessagePage{FormTitle: f.Title}
	switch forms.Availability(f, tenantActive, now) {
	case forms.Available:
		return nil
	case forms.NotStarted:
		msg.Title = "Not open yet"
		msg.Message = "This form opens on " + f.StartDate.UTC().Format(publicDateLayout) + "."
	case forms.Ended:
		msg.Title = "Form closed"
		msg.Message = "This form closed on " + f.EndDate.UTC().Format(publicDateLayout) + " and no longer accepts responses."
	default:
		return notAcceptingMessage(f)
	}
	return msg
}

// notAcceptingMessage returns the message page of a form that accepts no
// responses for a reason respondents need not know, such as being inactive
// or its tenant having reached a plan limit.
func notAcceptingMessage(f forms.Form) *publicMessagePage {
	return &publicMessagePage{FormTitle: f.Title, Title: "Not accepting responses", Message: msgNotAccepting}
}

// publicPage is the data of a public page template, which embeds publicLayout.
type publicPage interface {
	layout() *publicLayout
}

func (l *publicLayout) layout() *publicLayout { return l }

// renderPublicPage renders t with page, which it gives a fresh nonce, along
// with the security headers of the public form pages. Embed mode pages may be
// framed by any site; the others by none.
func renderPublicPage(w http.ResponseWriter, r *http.Request, status int, t *template.Template, page publicPage) {
	nonce, err := newNonce()
	if err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	layout := page.layout()
	layout.Nonce = nonce
	// Render to a buffer first so a template error still yields a clean 500.
	var buf bytes.Buffer
	if err := t.Execute(&buf, page); err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	ancestors := publicFrameAncestors
	if layout.Embed {
		ancestors = embedFrameAncestors
	}
	h.Set("Content-Security-Policy", fmt.Sprintf(publicContentSecurityPolicy, nonce, ancestors))
	setAppNoStoreHeaders(h)
	w.WriteHeader(status)
	buf.WriteTo(w)
}
