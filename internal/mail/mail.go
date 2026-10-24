// Package mail sends Payahub's own email: alerts to operators now, and
// notices to organisations later. Sending goes through a queued job, so a
// mail server that is down delays a message but does not lose it.
package mail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// Message is a plain-text email.
type Message struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

// Sender delivers a message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ErrInvalid is a message that can never be sent: retrying will not help.
var ErrInvalid = errors.New("mail: invalid message")

// Check refuses a message with no valid recipient or with a line break in a
// header field (header injection).
func Check(m Message) error {
	if len(m.To) == 0 {
		return fmt.Errorf("%w: no recipients", ErrInvalid)
	}
	for _, to := range m.To {
		if a, err := mail.ParseAddress(to); err != nil || a.Address != to {
			return fmt.Errorf("%w: bad recipient %q", ErrInvalid, to)
		}
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("%w: line break in subject", ErrInvalid)
	}
	return nil
}

// SMTP sends through a mail server, using STARTTLS when the server offers it.
type SMTP struct {
	Addr     string // host:port
	Username string
	Password string
	From     string // the sender address
}

// FromURL reads smtp://user:password@host:587; from is the sender address.
func FromURL(raw, from string) (*SMTP, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "smtp" || u.Hostname() == "" {
		return nil, errors.New("mail: the server is smtp://user:password@host:port")
	}
	if a, err := mail.ParseAddress(from); err != nil || a.Address != from {
		return nil, errors.New("mail: the sender address is not valid")
	}
	port := u.Port()
	if port == "" {
		port = "587"
	}
	s := &SMTP{Addr: net.JoinHostPort(u.Hostname(), port), From: from}
	if u.User != nil {
		s.Username = u.User.Username()
		s.Password, _ = u.User.Password()
	}
	return s, nil
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	if err := Check(m); err != nil {
		return err
	}
	var auth smtp.Auth
	if s.Username != "" {
		host, _, _ := net.SplitHostPort(s.Addr)
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}
	done := make(chan error, 1)
	go func() { done <- smtp.SendMail(s.Addr, auth, s.From, m.To, Compose(s.From, m, time.Now())) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Compose writes the message as RFC 5322 text.
func Compose(from string, m Message, at time.Time) []byte {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := from[strings.LastIndex(from, "@")+1:]
	var b strings.Builder
	fmt.Fprintf(&b, "From: Payahub <%s>\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(m.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", at.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	// Lines end in CRLF, and a line that is only "." is escaped by net/smtp.
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.Text, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String())
}

// Log writes messages to the log instead of sending them: the default when
// no mail server is set up (development, tests).
type Log struct {
	Logger *slog.Logger
	// Body logs the text as well, for development only: an invitation's
	// text carries its link.
	Body bool
}

func (l Log) Send(ctx context.Context, m Message) error {
	if err := Check(m); err != nil {
		return err
	}
	if l.Logger != nil {
		args := []any{"to", m.To, "subject", m.Subject}
		if l.Body {
			args = append(args, "text", m.Text)
		}
		l.Logger.InfoContext(ctx, "email not sent: no mail server is set up", args...)
	}
	return nil
}
