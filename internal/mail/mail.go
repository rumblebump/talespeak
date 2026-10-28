// Package mail sends Talespeak's few mails (password reset links, address
// confirmations) through an SMTP server the admin chooses, with Go's
// net/smtp: always encrypted, with STARTTLS or TLS from the start, never
// in plain text. Mails wait in an outbox in the store and are retried
// (Outbox), so a slow or unreachable mail server never blocks a request.
// See docs/admin-email.md.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// TLS modes: how the connection to the SMTP server is encrypted.
const (
	TLSStart = "starttls" // plain TCP first, then STARTTLS before anything else (port 587)
	TLSOn    = "tls"      // TLS from the first byte (port 465)
)

// Limits on a mail and on talking to the server.
const (
	// SendTimeout bounds one delivery, so a server that stops answering
	// cannot hold the outbox for long.
	SendTimeout = 30 * time.Second
	// MaxAddress is the longest address SMTP allows (RFC 5321).
	MaxAddress = 254
	// MaxSubject and MaxBody bound what Talespeak itself writes.
	MaxSubject = 200
	MaxBody    = 16 << 10
)

// Config is the SMTP server mail goes through.
type Config struct {
	Host     string
	Port     int
	TLS      string // TLSStart or TLSOn
	User     string // "" for a server without sign-in (a relay that trusts this host)
	Password string
	From     string // "Talespeak <no-reply@example.org>" or just the address
	// Hello is the name this side gives in EHLO; "" is "localhost".
	Hello string
	// RootCAs are the certificates the server's is checked against; nil
	// is the system's. Tests set their own.
	RootCAs *x509.CertPool
}

// Check reports what is wrong with c, or nil.
func (c Config) Check() error {
	var errs []error
	if c.Host == "" || strings.ContainsAny(c.Host, " /\r\n") {
		errs = append(errs, errors.New("an SMTP host is needed"))
	}
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("SMTP port %d is not a port", c.Port))
	}
	if c.TLS != TLSStart && c.TLS != TLSOn {
		errs = append(errs, fmt.Errorf("SMTP TLS %q: want starttls or tls; Talespeak never sends mail unencrypted", c.TLS))
	}
	if (c.User == "") != (c.Password == "") {
		errs = append(errs, errors.New("set both the SMTP user and password, or neither"))
	}
	if _, err := c.from(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// from is the From header's address.
func (c Config) from() (*mail.Address, error) {
	a, err := mail.ParseAddress(c.From)
	if err != nil || ValidAddress(a.Address) != nil {
		return nil, fmt.Errorf("the From address %q is not an email address", c.From)
	}
	return a, nil
}

// Message is one mail to one recipient, in plain text.
type Message struct {
	To      string
	Subject string
	Body    string
}

// ValidAddress says what is wrong with an email address as a person types
// it, or returns nil: a plain address of printable ASCII with one @ and a
// dot in the domain, without a display name or comments.
func ValidAddress(addr string) error {
	if addr == "" || len(addr) > MaxAddress {
		return errors.New("Email addresses are at most 254 characters long.")
	}
	for _, r := range addr {
		if r <= ' ' || r > '~' {
			return errors.New("That is not an email address.")
		}
	}
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" || strings.Contains(domain, "@") || !strings.Contains(domain, ".") ||
		strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return errors.New("That is not an email address.")
	}
	a, err := mail.ParseAddress(addr)
	if err != nil || a.Address != addr || a.Name != "" {
		return errors.New("That is not an email address.")
	}
	return nil
}

// Send delivers m through the server of c. Errors from the server carry
// its answer (for "talespeak mail test").
func Send(ctx context.Context, c Config, m Message) error {
	if err := c.Check(); err != nil {
		return err
	}
	from, _ := c.from()
	if err := ValidAddress(m.To); err != nil {
		return fmt.Errorf("mail: recipient: %w", err)
	}
	data, err := compose(from, m, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("mail: connect to %s: %w", c.addr(), err)
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return fmt.Errorf("mail: greeting: %w", err)
	}
	defer client.Close()
	hello := c.Hello
	if hello == "" {
		hello = "localhost"
	}
	if err := client.Hello(hello); err != nil {
		return fmt.Errorf("mail: EHLO: %w", err)
	}
	if c.TLS == TLSStart {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("mail: the server does not offer STARTTLS, and Talespeak never sends mail unencrypted")
		}
		if err := client.StartTLS(c.tlsConfig()); err != nil {
			return fmt.Errorf("mail: STARTTLS: %w", err)
		}
	}
	if c.User != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("mail: the server offers no sign-in (AUTH)")
		}
		if err := client.Auth(smtp.PlainAuth("", c.User, c.Password, c.Host)); err != nil {
			return fmt.Errorf("mail: authentication failed: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("mail: sender not accepted: %w", err)
	}
	if err := client.Rcpt(m.To); err != nil {
		return fmt.Errorf("mail: recipient not accepted: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("mail: sending the text: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: not accepted: %w", err)
	}
	return client.Quit()
}

func (c Config) addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

func (c Config) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, RootCAs: c.RootCAs}
}

// dial opens the connection: in TLS mode already encrypted.
func (c Config) dial(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if c.TLS == TLSOn {
		return (&tls.Dialer{NetDialer: d, Config: c.tlsConfig()}).DialContext(ctx, "tcp", c.addr())
	}
	return d.DialContext(ctx, "tcp", c.addr())
}

// compose writes m as a mail: headers, then the text in quoted-printable
// UTF-8 with CRLF line ends.
func compose(from *mail.Address, m Message, now time.Time) ([]byte, error) {
	if len(m.Subject) > MaxSubject || strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("mail: subject too long or not one line")
	}
	if len(m.Body) > MaxBody {
		return nil, errors.New("mail: text too long")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	_, domain, _ := strings.Cut(from.Address, "@")
	var b bytes.Buffer
	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	header("From", from.String())
	header("To", (&mail.Address{Address: m.To}).String())
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated") // no out-of-office replies
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	body := strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
