package mailer

import (
	"strings"
	"testing"
	"time"
)

func TestRenderEmailVerification(t *testing.T) {
	r, err := RenderEmailVerification("https://forms.example.com/", "tok en&value")
	if err != nil {
		t.Fatalf("RenderEmailVerification() error = %v", err)
	}
	// The text template does not re-encode its output, so the escaped query
	// value is visible verbatim there.
	wantLink := "https://forms.example.com/app/verify?token=tok+en%26value"
	if !strings.Contains(r.Text, wantLink) {
		t.Fatalf("Text = %q, want to contain %q", r.Text, wantLink)
	}
	// html/template additionally HTML-escapes the attribute value, so "+"
	// and "&" show up as numeric character references there.
	if !strings.Contains(r.HTML, `href="https://forms.example.com/app/verify?token=tok`) {
		t.Fatalf("HTML = %q, want an href starting with the verify URL", r.HTML)
	}
	if strings.Contains(r.HTML, "token=tok en&value") {
		t.Fatalf("HTML = %q, query value was not escaped", r.HTML)
	}
	if r.Subject == "" {
		t.Fatal("Subject is empty")
	}
}

func TestRenderPasswordReset(t *testing.T) {
	r, err := RenderPasswordReset("https://forms.example.com", "reset-token-123")
	if err != nil {
		t.Fatalf("RenderPasswordReset() error = %v", err)
	}
	wantLink := "https://forms.example.com/app/reset-password?token=reset-token-123"
	if !strings.Contains(r.Text, wantLink) {
		t.Fatalf("Text = %q, want to contain %q", r.Text, wantLink)
	}
	if !strings.Contains(r.HTML, wantLink) {
		t.Fatalf("HTML = %q, want to contain %q", r.HTML, wantLink)
	}
}

func TestRenderNewSubmissionNotificationEscapesHTML(t *testing.T) {
	submittedAt := time.Date(2026, 3, 4, 13, 5, 0, 0, time.UTC)
	title := `<script>alert("hi")</script> & Co`

	r, err := RenderNewSubmissionNotification("https://forms.example.com", "form-123", title, submittedAt)
	if err != nil {
		t.Fatalf("RenderNewSubmissionNotification() error = %v", err)
	}

	if !strings.Contains(r.Text, title) {
		t.Fatalf("Text = %q, want raw title %q", r.Text, title)
	}
	if strings.Contains(r.HTML, "<script>") {
		t.Fatalf("HTML = %q, contains unescaped <script>", r.HTML)
	}
	if !strings.Contains(r.HTML, "&lt;script&gt;") || !strings.Contains(r.HTML, "&amp;") {
		t.Fatalf("HTML = %q, want escaped title", r.HTML)
	}
	wantLink := "https://forms.example.com/app/forms/form-123"
	if !strings.Contains(r.Text, wantLink) || !strings.Contains(r.HTML, wantLink) {
		t.Fatalf("link missing from rendered output: text=%q html=%q", r.Text, r.HTML)
	}
	if !strings.Contains(r.Text, "Mar 4, 2026") {
		t.Fatalf("Text = %q, want formatted submission time", r.Text)
	}
}

func TestRenderPaymentFailed(t *testing.T) {
	r, err := RenderPaymentFailed("https://forms.example.com")
	if err != nil {
		t.Fatalf("RenderPaymentFailed() error = %v", err)
	}
	wantLink := "https://forms.example.com/app/billing"
	if !strings.Contains(r.Text, wantLink) {
		t.Fatalf("Text = %q, want to contain %q", r.Text, wantLink)
	}
	if !strings.Contains(r.HTML, wantLink) {
		t.Fatalf("HTML = %q, want to contain %q", r.HTML, wantLink)
	}
}

func TestAbsoluteURLTrimsTrailingSlash(t *testing.T) {
	tests := []struct {
		base string
		path string
		want string
	}{
		{"https://forms.example.com/", "/app/billing", "https://forms.example.com/app/billing"},
		{"https://forms.example.com", "/app/billing", "https://forms.example.com/app/billing"},
	}
	for _, tt := range tests {
		if got := absoluteURL(tt.base, tt.path); got != tt.want {
			t.Errorf("absoluteURL(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
		}
	}
}
