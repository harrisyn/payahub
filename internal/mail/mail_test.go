package mail

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCheckRefusesHeaderInjection(t *testing.T) {
	ok := Message{To: []string{"ops@payahub.test"}, Subject: "New enquiry", Text: "x"}
	if err := Check(ok); err != nil {
		t.Fatal(err)
	}
	for _, m := range []Message{
		{Subject: "s"},
		{To: []string{"ops@payahub.test\r\nBcc: x@evil.test"}, Subject: "s"},
		{To: []string{"Ops <ops@payahub.test>"}, Subject: "s"},
		{To: []string{"ops@payahub.test"}, Subject: "s\r\nBcc: x@evil.test"},
	} {
		if err := Check(m); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %+v", m)
		}
	}
}

func TestComposeIsWellFormed(t *testing.T) {
	b := string(Compose("no-reply@payahub.com", Message{To: []string{"a@x.test", "b@x.test"}, Subject: "Nouvelle demande — Kɔfi", Text: "line one\nline two\n"}, time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)))
	head, body, ok := strings.Cut(b, "\r\n\r\n")
	if !ok {
		t.Fatal("no blank line after the headers")
	}
	for _, want := range []string{"From: Payahub <no-reply@payahub.com>", "To: a@x.test, b@x.test", "Subject: =?utf-8?q?", "Date: Sat, 24 Oct 2026 09:00:00 +0000", "@payahub.com>", "Content-Type: text/plain; charset=utf-8"} {
		if !strings.Contains(head, want) {
			t.Errorf("missing %q in\n%s", want, head)
		}
	}
	if body != "line one\r\nline two\r\n" {
		t.Errorf("body %q", body)
	}
}

func TestFromURL(t *testing.T) {
	s, err := FromURL("smtp://apikey:s3cr%2Ft@smtp.example.com", "no-reply@payahub.com")
	if err != nil || s.Addr != "smtp.example.com:587" || s.Username != "apikey" || s.Password != "s3cr/t" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := FromURL("https://smtp.example.com", "no-reply@payahub.com"); err == nil {
		t.Error("accepted a non-smtp URL")
	}
	if _, err := FromURL("smtp://smtp.example.com:25", "not an address"); err == nil {
		t.Error("accepted a bad sender")
	}
}

// A minimal mail server: enough of SMTP for net/smtp to deliver one message.
func fakeServer(t *testing.T) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		say := func(s string) { c.Write([]byte(s + "\r\n")) }
		say("220 fake ready")
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				data.WriteString(strings.TrimSpace(line) + "\n")
				say("250 ok")
			case cmd == "DATA":
				say("354 go on")
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				say("250 queued")
			case cmd == "QUIT":
				say("221 bye")
				got <- data.String()
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPDelivers(t *testing.T) {
	addr, got := fakeServer(t)
	s := &SMTP{Addr: addr, From: "no-reply@payahub.com"}
	if err := s.Send(context.Background(), Message{To: []string{"ops@payahub.test"}, Subject: "New enquiry", Text: "Hello\n.\nend"}); err != nil {
		t.Fatal(err)
	}
	d := <-got
	for _, want := range []string{"MAIL FROM:<no-reply@payahub.com>", "RCPT TO:<ops@payahub.test>", "Subject: New enquiry", "Hello\r\n..\r\nend"} {
		if !strings.Contains(d, want) {
			t.Errorf("missing %q in\n%s", want, d)
		}
	}
}
