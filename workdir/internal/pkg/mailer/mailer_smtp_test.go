package mailer

import (
	"bufio"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestNewSMTPMailer(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SMTPConfig
		wantErr bool
	}{
		{"valid", SMTPConfig{Host: "smtp.example.com", Port: 587, From: "GoGoForm <no-reply@example.com>"}, false},
		{"missing host", SMTPConfig{Port: 587, From: "no-reply@example.com"}, true},
		{"port too low", SMTPConfig{Host: "smtp.example.com", Port: 0, From: "no-reply@example.com"}, true},
		{"port too high", SMTPConfig{Host: "smtp.example.com", Port: 65536, From: "no-reply@example.com"}, true},
		{"invalid from", SMTPConfig{Host: "smtp.example.com", Port: 587, From: "not-an-address"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewSMTPMailer(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewSMTPMailer(%+v) error = %v, wantErr %v", tt.cfg, err, tt.wantErr)
			}
			if err == nil && m.cfg.Timeout != defaultSMTPTimeout {
				t.Fatalf("Timeout = %v, want default %v", m.cfg.Timeout, defaultSMTPTimeout)
			}
		})
	}
}

func TestBuildMessageTextAndHTML(t *testing.T) {
	from, err := mail.ParseAddress("GoGoForm <no-reply@example.com>")
	if err != nil {
		t.Fatalf("ParseAddress(from) error = %v", err)
	}
	to, err := mail.ParseAddress("user@example.com")
	if err != nil {
		t.Fatalf("ParseAddress(to) error = %v", err)
	}
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	msg := Message{
		To:      to.Address,
		Subject: "Verify your email",
		Text:    "Visit https://example.com/verify?token=abc to verify your café account.",
		HTML:    "<p>Visit <a href=\"https://example.com/verify?token=abc\">this link</a> to verify your café account.</p>",
	}

	raw, err := buildMessage(from, to, msg, now)
	if err != nil {
		t.Fatalf("buildMessage() error = %v", err)
	}
	body := string(raw)

	if !strings.Contains(body, "\r\n") {
		t.Fatal("buildMessage() body does not use CRLF line endings")
	}

	header, rest := splitHeader(t, raw)
	if got := header.Get("From"); got != from.String() {
		t.Fatalf("From = %q, want %q", got, from.String())
	}
	if got := header.Get("To"); got != to.String() {
		t.Fatalf("To = %q, want %q", got, to.String())
	}
	wantSubject := mime.QEncoding.Encode("utf-8", msg.Subject)
	if got := header.Get("Subject"); got != wantSubject {
		t.Fatalf("Subject = %q, want %q", got, wantSubject)
	}
	if got := header.Get("Date"); got != now.Format(time.RFC1123Z) {
		t.Fatalf("Date = %q, want %q", got, now.Format(time.RFC1123Z))
	}
	msgID := header.Get("Message-ID")
	if !strings.HasPrefix(msgID, "<") || !strings.HasSuffix(msgID, "@example.com>") {
		t.Fatalf("Message-ID = %q, want <...@example.com>", msgID)
	}
	if got := header.Get("MIME-Version"); got != "1.0" {
		t.Fatalf("MIME-Version = %q, want 1.0", got)
	}

	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType(Content-Type) error = %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q, want multipart/alternative", mediaType)
	}

	mr := multipart.NewReader(strings.NewReader(rest), params["boundary"])
	var parts []struct {
		contentType string
		body        string
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart() error = %v", err)
		}
		// multipart.Part already decodes Content-Transfer-Encoding: quoted-printable.
		decoded, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		parts = append(parts, struct {
			contentType string
			body        string
		}{part.Header.Get("Content-Type"), string(decoded)})
	}
	if len(parts) != 2 {
		t.Fatalf("got %d MIME parts, want 2", len(parts))
	}
	if !strings.HasPrefix(parts[0].contentType, "text/plain") || parts[0].body != msg.Text {
		t.Fatalf("part[0] = %+v, want text/plain with body %q", parts[0], msg.Text)
	}
	if !strings.HasPrefix(parts[1].contentType, "text/html") || parts[1].body != msg.HTML {
		t.Fatalf("part[1] = %+v, want text/html with body %q", parts[1], msg.HTML)
	}
}

func TestBuildMessageTextOnly(t *testing.T) {
	from, _ := mail.ParseAddress("no-reply@example.com")
	to, _ := mail.ParseAddress("user@example.com")
	msg := Message{To: to.Address, Subject: "Hi", Text: "plain text only"}

	raw, err := buildMessage(from, to, msg, time.Now())
	if err != nil {
		t.Fatalf("buildMessage() error = %v", err)
	}
	header, rest := splitHeader(t, raw)
	if got := header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(rest)))
	if err != nil {
		t.Fatalf("decode quoted-printable body: %v", err)
	}
	if string(decoded) != msg.Text {
		t.Fatalf("body = %q, want %q", decoded, msg.Text)
	}
}

func TestBuildMessageUniqueMessageID(t *testing.T) {
	from, _ := mail.ParseAddress("no-reply@example.com")
	to, _ := mail.ParseAddress("user@example.com")
	msg := Message{To: to.Address, Subject: "Hi", Text: "hello"}

	raw1, err := buildMessage(from, to, msg, time.Now())
	if err != nil {
		t.Fatalf("buildMessage() error = %v", err)
	}
	raw2, err := buildMessage(from, to, msg, time.Now())
	if err != nil {
		t.Fatalf("buildMessage() error = %v", err)
	}
	h1, _ := splitHeader(t, raw1)
	h2, _ := splitHeader(t, raw2)
	if h1.Get("Message-ID") == h2.Get("Message-ID") {
		t.Fatal("Message-ID repeated across two messages, want unique ids")
	}
}

// splitHeader parses the RFC 5322 header block of raw and returns it along
// with the remaining body.
func splitHeader(t *testing.T, raw []byte) (textproto.MIMEHeader, string) {
	t.Helper()
	tp := textproto.NewReader(bufio.NewReader(strings.NewReader(string(raw))))
	header, err := tp.ReadMIMEHeader()
	if err != nil && err != io.EOF {
		t.Fatalf("ReadMIMEHeader() error = %v", err)
	}
	body, err := io.ReadAll(tp.R)
	if err != nil {
		t.Fatalf("read remaining body: %v", err)
	}
	return header, string(body)
}
