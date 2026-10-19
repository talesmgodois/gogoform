package mailer

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	"net/url"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed templates/*.txt templates/*.html
var templatesFS embed.FS

var (
	textTemplates = texttemplate.Must(texttemplate.ParseFS(templatesFS, "templates/*.txt"))
	htmlTemplates = htmltemplate.Must(htmltemplate.ParseFS(templatesFS, "templates/*.html"))
)

// Rendered is the subject and both bodies of a rendered email, ready to be
// combined with a recipient into a Message.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// absoluteURL joins baseURL (e.g. "https://forms.example.com") with a
// path and optional query, e.g. "/app/verify?token=abc".
func absoluteURL(baseURL, pathAndQuery string) string {
	return strings.TrimRight(baseURL, "/") + pathAndQuery
}

func render(name string, data any) (Rendered, error) {
	var textBuf, htmlBuf bytes.Buffer
	if err := textTemplates.ExecuteTemplate(&textBuf, name+".txt", data); err != nil {
		return Rendered{}, err
	}
	if err := htmlTemplates.ExecuteTemplate(&htmlBuf, name+".html", data); err != nil {
		return Rendered{}, err
	}
	return Rendered{Text: textBuf.String(), HTML: htmlBuf.String()}, nil
}

type verificationData struct {
	VerifyURL string
}

// RenderEmailVerification renders the email-verification message. token is
// the plaintext token the user has to present at GET /app/verify?token=.
func RenderEmailVerification(baseURL, token string) (Rendered, error) {
	r, err := render("verification", verificationData{
		VerifyURL: absoluteURL(baseURL, "/app/verify?token="+url.QueryEscape(token)),
	})
	if err != nil {
		return Rendered{}, err
	}
	r.Subject = "Verify your email address"
	return r, nil
}

type passwordResetData struct {
	ResetURL string
}

// RenderPasswordReset renders the password-reset message. token is the
// plaintext token the user has to present at GET /app/reset-password?token=.
func RenderPasswordReset(baseURL, token string) (Rendered, error) {
	r, err := render("password_reset", passwordResetData{
		ResetURL: absoluteURL(baseURL, "/app/reset-password?token="+url.QueryEscape(token)),
	})
	if err != nil {
		return Rendered{}, err
	}
	r.Subject = "Reset your password"
	return r, nil
}

type newSubmissionData struct {
	FormTitle   string
	SubmittedAt string
	FormURL     string
}

// RenderNewSubmissionNotification renders the message a pro form owner gets
// when their form receives a new submission.
func RenderNewSubmissionNotification(baseURL, formID, formTitle string, submittedAt time.Time) (Rendered, error) {
	r, err := render("new_submission", newSubmissionData{
		FormTitle:   formTitle,
		SubmittedAt: submittedAt.Format("Jan 2, 2006 3:04 PM MST"),
		FormURL:     absoluteURL(baseURL, "/app/forms/"+url.PathEscape(formID)),
	})
	if err != nil {
		return Rendered{}, err
	}
	r.Subject = "New submission on " + formTitle
	return r, nil
}

type paymentFailedData struct {
	BillingURL string
}

// RenderPaymentFailed renders the billing message sent when a subscription
// payment fails.
func RenderPaymentFailed(baseURL string) (Rendered, error) {
	r, err := render("payment_failed", paymentFailedData{
		BillingURL: absoluteURL(baseURL, "/app/billing"),
	})
	if err != nil {
		return Rendered{}, err
	}
	r.Subject = "Your payment failed"
	return r, nil
}
