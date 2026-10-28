package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/mail"
)

// mailSettings are the mail settings (docs/admin-email.md): whether the
// server sends mail, the SMTP server, and the address reset links start
// with.
type mailSettings struct {
	email     bool // TALESPEAK_EMAIL_REGISTRATION
	smtp      mail.Config
	hasSMTP   bool // TALESPEAK_SMTP_HOST is set
	publicURL string
}

// mailRoots are the certificates an SMTP server's is checked against;
// nil is the system's. Tests set their own.
var mailRoots *x509.CertPool

// loadMail reads the mail settings. Email needs an SMTP server and the
// public address; the SMTP server alone is enough for "mail test". Every
// wrong value is reported at once, and the password never is.
func loadMail(getenv func(string) string) (mailSettings, error) {
	var m mailSettings
	var errs []error
	switch v := getenv("TALESPEAK_EMAIL_REGISTRATION"); v {
	case "", "false":
	case "true":
		m.email = true
	default:
		errs = append(errs, fmt.Errorf("TALESPEAK_EMAIL_REGISTRATION=%q: want true or false", v))
	}
	if v := getenv("TALESPEAK_PUBLIC_URL"); v != "" {
		u, err := publicURL(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("TALESPEAK_PUBLIC_URL=%q: %w", v, err))
		}
		m.publicURL = u
	}
	c := mail.Config{Host: getenv("TALESPEAK_SMTP_HOST"), TLS: getenv("TALESPEAK_SMTP_TLS"),
		User: getenv("TALESPEAK_SMTP_USER"), Password: getenv("TALESPEAK_SMTP_PASSWORD"), From: getenv("TALESPEAK_SMTP_FROM")}
	m.hasSMTP = c.Host != ""
	if c.TLS == "" {
		c.TLS = mail.TLSStart
	}
	c.Port = 587
	if c.TLS == mail.TLSOn {
		c.Port = 465
	}
	if v := getenv("TALESPEAK_SMTP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			errs = append(errs, fmt.Errorf("TALESPEAK_SMTP_PORT=%q: want a port from 1 to 65535", v))
		}
		c.Port = p
	}
	if m.publicURL != "" {
		u, _ := url.Parse(m.publicURL)
		c.Hello = u.Hostname()
	}
	c.RootCAs = mailRoots
	m.smtp = c
	if m.hasSMTP {
		if err := c.Check(); err != nil {
			errs = append(errs, fmt.Errorf("TALESPEAK_SMTP_*: %w", err))
		}
	}
	if m.email {
		if !m.hasSMTP {
			errs = append(errs, errors.New("TALESPEAK_EMAIL_REGISTRATION=true needs TALESPEAK_SMTP_HOST and the other SMTP settings"))
		}
		if m.publicURL == "" {
			errs = append(errs, errors.New("TALESPEAK_EMAIL_REGISTRATION=true needs TALESPEAK_PUBLIC_URL, the address the links in mails start with"))
		}
	}
	return m, errors.Join(errs...)
}

// publicURL checks the address players open the server at: https (http
// only for this machine, to try it out), no user, query or fragment. A
// path is kept, without its last slash.
func publicURL(v string) (string, error) {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("want an address like https://play.example.org")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", errors.New("want https (http only for localhost)")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

// mailCmd runs "talespeak mail test ADDRESS": it sends one mail through
// the SMTP server in the settings, at once and outside the outbox, and
// says what the server answered.
func mailCmd(args []string, getenv func(string) string, stdout io.Writer) error {
	if len(args) != 2 || args[0] != "test" {
		return errors.New("mail: want \"mail test ADDRESS\"")
	}
	to := args[1]
	if err := mail.ValidAddress(to); err != nil {
		return fmt.Errorf("mail test: %w", err)
	}
	m, err := loadMail(getenv)
	if err != nil {
		return err
	}
	if !m.hasSMTP {
		return errors.New("mail test: set TALESPEAK_SMTP_HOST and the other SMTP settings first (docs/admin-email.md)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), mail.SendTimeout+10*time.Second)
	defer cancel()
	msg := mail.Message{To: to, Subject: "Talespeak test mail",
		Body: "This is a test mail from your Talespeak server. If you read it, its mail settings work.\n"}
	if err := mail.Send(ctx, m.smtp, msg); err != nil {
		return fmt.Errorf("mail test: %w", err)
	}
	fmt.Fprintf(stdout, "The SMTP server %s:%d (%s) accepted the test mail. If it does not arrive, look in the spam folder and check SPF, DKIM and DMARC.\n",
		m.smtp.Host, m.smtp.Port, m.smtp.TLS)
	return nil
}
