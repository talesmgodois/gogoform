package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// defaultSMTPTimeout bounds a delivery when the context has no earlier deadline.
const defaultSMTPTimeout = 30 * time.Second

// SMTPConfig is what SMTPMailer needs to reach the relay.
type SMTPConfig struct {
	Host string
	Port int
	// User and Password enable PLAIN auth when User is set.
	User     string
	Password string
	// From is the sender, e.g. "GoGoForm <no-reply@example.com>".
	From string
	// Timeout bounds a single delivery; zero means 30s.
	Timeout time.Duration
}

var _ Mailer = (*SMTPMailer)(nil)

// SMTPMailer delivers email through an SMTP relay. Port 465 uses implicit
// TLS; ports 587 and 25 require STARTTLS; any other port (e.g. a local dev
// catcher) upgrades with STARTTLS when the server offers it.
type SMTPMailer struct {
	cfg  SMTPConfig
	from *mail.Address
	// tlsConfig is used for implicit TLS and STARTTLS; tests may override it.
	tlsConfig *tls.Config
	now       func() time.Time
}

// NewSMTPMailer validates cfg and returns an SMTPMailer.
func NewSMTPMailer(cfg SMTPConfig) (*SMTPMailer, error) {
	if cfg.Host == "" {
		return nil, errors.New("mailer: smtp host is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("mailer: smtp port %d out of range 1-65535", cfg.Port)
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mailer: invalid from address %q: %w", cfg.From, err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultSMTPTimeout
	}
	return &SMTPMailer{
		cfg:       cfg,
		from:      from,
		tlsConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
		now:       time.Now,
	}, nil
}

// Send delivers msg, honouring ctx cancellation and the configured timeout.
func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	if err := msg.validate(); err != nil {
		return err
	}
	to, _ := mail.ParseAddress(msg.To) // validated above
	body, err := buildMessage(m.from, to, msg, m.now())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, m.cfg.Timeout)
	defer cancel()
	if err := m.deliver(ctx, to.Address, body); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("mailer: smtp send: %w (%v)", ctxErr, err)
		}
		return fmt.Errorf("mailer: smtp send: %w", err)
	}
	return nil
}

func (m *SMTPMailer) deliver(ctx context.Context, rcpt string, body []byte) error {
	conn, err := m.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Unblock any pending read or write once ctx is done.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()

	if m.cfg.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(m.tlsConfig); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		} else if m.cfg.Port == 587 || m.cfg.Port == 25 {
			return errors.New("server does not support STARTTLS")
		}
	}
	if m.cfg.User != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("server does not support AUTH")
		}
		if err := c.Auth(smtp.PlainAuth("", m.cfg.User, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := c.Mail(m.from.Address); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(rcpt); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("data: %w", err)
	}
	return c.Quit()
}

func (m *SMTPMailer) dial(ctx context.Context) (net.Conn, error) {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	if m.cfg.Port == 465 {
		d := &tls.Dialer{Config: m.tlsConfig}
		return d.DialContext(ctx, "tcp", addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

// buildMessage renders msg as an RFC 5322 message with CRLF line endings: a
// multipart/alternative text+HTML body, or a single text/plain part when
// msg has no HTML. Both parts are UTF-8 and quoted-printable encoded.
func buildMessage(from, to *mail.Address, msg Message, now time.Time) ([]byte, error) {
	msgID, err := newMessageID(from.Address)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	header := func(k, v string) { buf.WriteString(k + ": " + v + "\r\n") }
	header("From", from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", msgID)
	header("MIME-Version", "1.0")

	if msg.HTML == "" {
		header("Content-Type", `text/plain; charset="utf-8"`)
		header("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQuotedPrintable(&buf, msg.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mw := multipart.NewWriter(&buf)
	header("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": mw.Boundary()}))
	buf.WriteString("\r\n")
	for _, part := range []struct{ contentType, body string }{
		{"text/plain", msg.Text},
		{"text/html", msg.HTML},
	} {
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.contentType + `; charset="utf-8"`},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		if err := writeQuotedPrintable(w, part.body); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeQuotedPrintable(w io.Writer, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(s)); err != nil {
		return err
	}
	return qp.Close()
}

// newMessageID returns a unique Message-ID in the sender's domain.
func newMessageID(fromAddr string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mailer: message id: %w", err)
	}
	domain := "localhost"
	if i := strings.LastIndexByte(fromAddr, '@'); i >= 0 && i < len(fromAddr)-1 {
		domain = fromAddr[i+1:]
	}
	return "<" + hex.EncodeToString(b[:]) + "@" + domain + ">", nil
}
