package mail

import (
	"context"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/mail/mailtest"
)

// config is a Config for the fake server s.
func config(s *mailtest.Server, tlsMode string) Config {
	return Config{Host: s.Host, Port: s.Port, TLS: tlsMode, User: "talespeak", Password: "secret", From: "Talespeak <no-reply@example.org>", RootCAs: s.RootCAs}
}

func TestSend(t *testing.T) {
	for _, mode := range []string{TLSStart, TLSOn} {
		t.Run(mode, func(t *testing.T) {
			s := mailtest.Start(t, mailtest.Options{TLS: mode, User: "talespeak", Password: "secret"})
			msg := Message{To: "mira@example.org", Subject: "Reset your Talespeak password ✓", Body: "Line one\nA long line that is longer than seventy-six characters so quoted-printable has to wrap it.\n"}
			if err := Send(context.Background(), config(s, mode), msg); err != nil {
				t.Fatal(err)
			}
			got := s.Mails()
			if len(got) != 1 {
				t.Fatalf("mails = %d", len(got))
			}
			m := got[0]
			if !m.TLS || !m.Auth || m.From != "no-reply@example.org" || m.To != "mira@example.org" {
				t.Errorf("mail = %+v", m)
			}
			parsed, err := mail.ReadMessage(strings.NewReader(m.Data))
			if err != nil {
				t.Fatal(err)
			}
			dec := new(mime.WordDecoder)
			if subj, _ := dec.DecodeHeader(parsed.Header.Get("Subject")); subj != msg.Subject {
				t.Errorf("Subject = %q", subj)
			}
			for k, want := range map[string]string{"To": "<mira@example.org>", "Content-Type": "text/plain; charset=utf-8", "Auto-Submitted": "auto-generated"} {
				if got := parsed.Header.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if !strings.HasSuffix(parsed.Header.Get("Message-ID"), "@example.org>") {
				t.Errorf("Message-ID = %q", parsed.Header.Get("Message-ID"))
			}
			body, _ := io.ReadAll(quotedprintable.NewReader(parsed.Body))
			if string(body) != msg.Body { // the server reads CRLF as LF
				t.Errorf("body = %q", body)
			}
		})
	}
}

func TestSendRefuses(t *testing.T) {
	ctx := context.Background()
	t.Run("no STARTTLS", func(t *testing.T) {
		s := mailtest.Start(t, mailtest.Options{NoStartTLS: true, User: "talespeak", Password: "secret"})
		err := Send(ctx, config(s, TLSStart), Message{To: "mira@example.org", Subject: "s", Body: "b"})
		if err == nil || !strings.Contains(err.Error(), "STARTTLS") || len(s.Mails()) != 0 {
			t.Fatalf("Send without STARTTLS = %v, %d mails; want a refusal", err, len(s.Mails()))
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		s := mailtest.Start(t, mailtest.Options{User: "talespeak", Password: "other"})
		err := Send(ctx, config(s, TLSStart), Message{To: "mira@example.org", Subject: "s", Body: "b"})
		if err == nil || !strings.Contains(err.Error(), "authentication failed") {
			t.Fatalf("Send = %v", err)
		}
	})
	t.Run("recipient rejected", func(t *testing.T) {
		s := mailtest.Start(t, mailtest.Options{User: "talespeak", Password: "secret", Reject: "nobody@example.org"})
		err := Send(ctx, config(s, TLSStart), Message{To: "nobody@example.org", Subject: "s", Body: "b"})
		if err == nil || !strings.Contains(err.Error(), "550") {
			t.Fatalf("Send = %v, want the server's answer", err)
		}
	})
	t.Run("untrusted certificate", func(t *testing.T) {
		s := mailtest.Start(t, mailtest.Options{TLS: TLSOn})
		c := config(s, TLSOn)
		c.RootCAs = nil // the system's: they do not know the test certificate
		c.User, c.Password = "", ""
		if err := Send(ctx, c, Message{To: "mira@example.org", Subject: "s", Body: "b"}); err == nil {
			t.Fatal("Send trusted an unknown certificate")
		}
	})
	t.Run("nobody listening", func(t *testing.T) {
		c := Config{Host: "127.0.0.1", Port: 1, TLS: TLSStart, From: "a@example.org"}
		if err := Send(ctx, c, Message{To: "mira@example.org", Subject: "s", Body: "b"}); err == nil || !strings.Contains(err.Error(), "connect") {
			t.Fatalf("Send = %v", err)
		}
	})
	t.Run("bad message", func(t *testing.T) {
		s := mailtest.Start(t, mailtest.Options{User: "talespeak", Password: "secret"})
		for _, m := range []Message{
			{To: "not an address", Subject: "s", Body: "b"},
			{To: "mira@example.org\r\nBcc: x@example.org", Subject: "s", Body: "b"},
			{To: "mira@example.org", Subject: "two\r\nBcc: x@example.org", Body: "b"},
			{To: "mira@example.org", Subject: strings.Repeat("s", MaxSubject+1), Body: "b"},
			{To: "mira@example.org", Subject: "s", Body: strings.Repeat("b", MaxBody+1)},
		} {
			if err := Send(ctx, config(s, TLSStart), m); err == nil {
				t.Errorf("Send(%q, %q) = nil, want an error", m.To, m.Subject[:min(len(m.Subject), 20)])
			}
		}
		if len(s.Mails()) != 0 {
			t.Errorf("mails sent: %d", len(s.Mails()))
		}
	})
}

func TestCheck(t *testing.T) {
	good := Config{Host: "smtp.example.org", Port: 587, TLS: TLSStart, User: "u", Password: "p", From: "Talespeak <no-reply@example.org>"}
	if err := good.Check(); err != nil {
		t.Fatalf("Check(good) = %v", err)
	}
	for name, change := range map[string]func(*Config){
		"no host":       func(c *Config) { c.Host = "" },
		"port":          func(c *Config) { c.Port = 0 },
		"big port":      func(c *Config) { c.Port = 70000 },
		"plain":         func(c *Config) { c.TLS = "none" },
		"empty tls":     func(c *Config) { c.TLS = "" },
		"user only":     func(c *Config) { c.Password = "" },
		"password only": func(c *Config) { c.User = "" },
		"bad from":      func(c *Config) { c.From = "no-reply" },
		"from newline":  func(c *Config) { c.From = "a@example.org\r\nBcc: b@example.org" },
	} {
		c := good
		change(&c)
		if err := c.Check(); err == nil {
			t.Errorf("Check(%s) = nil", name)
		}
	}
}

func TestValidAddress(t *testing.T) {
	for _, ok := range []string{"mira@example.org", "a.b+c@sub.example.co.uk", "X@Y.Z"} {
		if err := ValidAddress(ok); err != nil {
			t.Errorf("ValidAddress(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "mira", "mira@", "@example.org", "mira@example", "mira@@example.org", "a@b@c.org",
		"Mira <mira@example.org>", "mira @example.org", "mira@example.org\n", "mïra@example.org", "mira@.org", "mira@org.",
		strings.Repeat("a", 250) + "@b.org", "(comment)mira@example.org"} {
		if err := ValidAddress(bad); err == nil {
			t.Errorf("ValidAddress(%q) = nil", bad)
		}
	}
}

func TestComposeDate(t *testing.T) {
	from := &mail.Address{Name: "Talespeak", Address: "no-reply@example.org"}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	data, err := compose(from, Message{To: "a@example.org", Subject: "Hi", Body: "x"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Date: Sat, 24 Oct 2026 12:00:00 +0000\r\n") {
		t.Errorf("no Date header: %q", data)
	}
}
