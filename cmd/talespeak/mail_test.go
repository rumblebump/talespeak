package main

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumblebump/talespeak/internal/account"
	"github.com/rumblebump/talespeak/internal/mail"
	"github.com/rumblebump/talespeak/internal/mail/mailtest"
)

// smtpEnv are working SMTP settings.
var smtpEnv = map[string]string{
	"TALESPEAK_SMTP_HOST": "smtp.example.org", "TALESPEAK_SMTP_USER": "talespeak", "TALESPEAK_SMTP_PASSWORD": "$SMTP_PASSWORD",
	"TALESPEAK_SMTP_FROM": "Talespeak <no-reply@example.org>",
}

func with(base map[string]string, kv ...string) map[string]string {
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestLoadMail(t *testing.T) {
	m, err := loadMail(env(nil))
	if err != nil || m.email || m.hasSMTP {
		t.Fatalf("defaults = %+v, %v", m, err)
	}
	m, err = loadMail(env(with(smtpEnv, "TALESPEAK_EMAIL_REGISTRATION", "true", "TALESPEAK_PUBLIC_URL", "https://play.example.org/")))
	if err != nil || !m.email || !m.hasSMTP || m.publicURL != "https://play.example.org" || m.smtp.Port != 587 ||
		m.smtp.TLS != mail.TLSStart || m.smtp.Hello != "play.example.org" {
		t.Fatalf("email on = %+v, %v", m, err)
	}
	if m, _ := loadMail(env(with(smtpEnv, "TALESPEAK_SMTP_TLS", "tls"))); m.smtp.Port != 465 {
		t.Errorf("tls port = %d, want 465", m.smtp.Port)
	}
	if m, _ := loadMail(env(with(smtpEnv, "TALESPEAK_SMTP_TLS", "tls", "TALESPEAK_SMTP_PORT", "2465"))); m.smtp.Port != 2465 {
		t.Errorf("port = %d", m.smtp.Port)
	}
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"email without SMTP":    {map[string]string{"TALESPEAK_EMAIL_REGISTRATION": "true", "TALESPEAK_PUBLIC_URL": "https://a.org"}, "needs TALESPEAK_SMTP_HOST"},
		"email without URL":     {with(smtpEnv, "TALESPEAK_EMAIL_REGISTRATION", "true"), "needs TALESPEAK_PUBLIC_URL"},
		"bad switch":            {map[string]string{"TALESPEAK_EMAIL_REGISTRATION": "yes"}, "want true or false"},
		"plain text":            {with(smtpEnv, "TALESPEAK_SMTP_TLS", "none"), "never sends mail unencrypted"},
		"bad port":              {with(smtpEnv, "TALESPEAK_SMTP_PORT", "0"), "TALESPEAK_SMTP_PORT"},
		"no from":               {with(smtpEnv, "TALESPEAK_SMTP_FROM", ""), "From address"},
		"user without secret":   {with(smtpEnv, "TALESPEAK_SMTP_PASSWORD", ""), "both"},
		"http public address":   {map[string]string{"TALESPEAK_PUBLIC_URL": "http://play.example.org"}, "https"},
		"public URL with query": {map[string]string{"TALESPEAK_PUBLIC_URL": "https://a.org/?x=1"}, "TALESPEAK_PUBLIC_URL"},
	} {
		_, err := loadMail(env(c.env))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "$SMTP_PASSWORD") {
			t.Errorf("%s: the password is in the error: %v", name, err)
		}
	}
}

func TestPublicURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://play.example.org":        "https://play.example.org",
		"https://play.example.org/":       "https://play.example.org",
		"https://example.org/talespeak/":  "https://example.org/talespeak",
		"https://play.example.org:8443":   "https://play.example.org:8443",
		"http://localhost:8080":           "http://localhost:8080",
		"http://127.0.0.1:8080/":          "http://127.0.0.1:8080",
		"http://example.org":              "",
		"https://user:pw@example.org":     "",
		"https://example.org/#x":          "",
		"play.example.org":                "",
		"javascript:alert(1)":             "",
		"https://":                        "",
		"ftp://example.org":               "",
		"https://example.org/?next=a.org": "",
	} {
		got, err := publicURL(in)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("publicURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestLoadClassCode(t *testing.T) {
	self := access{registration: account.RegistrationSelf}
	if c, err := loadClassCode(env(nil), self); c != "" || err != nil {
		t.Errorf("unset = %q, %v", c, err)
	}
	if c, err := loadClassCode(env(map[string]string{"TALESPEAK_CLASS_CODE": "Owl Tree-7"}), self); c != "Owl Tree-7" || err != nil {
		t.Errorf("set = %q, %v", c, err)
	}
	for _, c := range []struct {
		code string
		a    access
	}{
		{"owl", self}, {strings.Repeat("a", 65), self}, {"owl tree!", self}, {"owltree7", access{registration: account.RegistrationOff}},
	} {
		got, err := loadClassCode(env(map[string]string{"TALESPEAK_CLASS_CODE": c.code}), c.a)
		if err == nil {
			t.Errorf("loadClassCode(%q, %s) = %q", c.code, c.a.registration, got)
		} else if strings.Contains(err.Error(), c.code) {
			t.Errorf("the code is in the error: %v", err)
		}
	}
}

// "talespeak mail test" sends one mail through the fake SMTP server.
func TestMailCommand(t *testing.T) {
	integration(t)
	srv := mailtest.Start(t, mailtest.Options{User: "talespeak", Password: "secret"})
	mailRoots = srv.RootCAs
	t.Cleanup(func() { mailRoots = nil })
	vars := map[string]string{"TALESPEAK_SMTP_HOST": srv.Host, "TALESPEAK_SMTP_PORT": strconv.Itoa(srv.Port),
		"TALESPEAK_SMTP_USER": "talespeak", "TALESPEAK_SMTP_PASSWORD": "secret", "TALESPEAK_SMTP_FROM": "no-reply@example.org"}
	var out bytes.Buffer
	if err := mailCmd([]string{"test", "carl@example.org"}, env(vars), &out); err != nil {
		t.Fatal(err)
	}
	if got := srv.Mails(); len(got) != 1 || got[0].To != "carl@example.org" || !got[0].TLS || !strings.Contains(got[0].Data, "Talespeak test mail") {
		t.Fatalf("mails = %+v", got)
	}
	if !strings.Contains(out.String(), "accepted") || strings.Contains(out.String(), "secret") {
		t.Errorf("output = %q", out.String())
	}
	for _, args := range [][]string{nil, {"test"}, {"send", "a@example.org"}, {"test", "not an address"}} {
		if err := mailCmd(args, env(vars), &out); err == nil {
			t.Errorf("mail %q = nil", args)
		}
	}
	if err := mailCmd([]string{"test", "a@example.org"}, env(nil), &out); err == nil || !strings.Contains(err.Error(), "TALESPEAK_SMTP_HOST") {
		t.Errorf("without settings: %v", err)
	}
	vars["TALESPEAK_SMTP_PASSWORD"] = "wrong"
	if err := mailCmd([]string{"test", "a@example.org"}, env(vars), &out); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("wrong password: %v", err)
	}
}

func TestAccountResetCode(t *testing.T) {
	integration(t)
	dir := t.TempDir()
	if _, err := runAccount(t, "correct horse\n", "add", "-data", dir, "Mira"); err != nil {
		t.Fatal(err)
	}
	out, err := runAccount(t, "", "resetcode", "-data", dir, "mira")
	m := regexp.MustCompile(`^([0-9A-Z]{5}-[0-9A-Z]{5})  \(reset code of Mira, works once, until (\S+)\)\n$`).FindStringSubmatch(out)
	if err != nil || m == nil {
		t.Fatalf("resetcode = %q, %v", out, err)
	}
	until, err := time.Parse(time.RFC3339, m[2])
	if err != nil || until.Sub(time.Now()) > account.ResetCodeTTL || until.Sub(time.Now()) < account.ResetCodeTTL-time.Minute {
		t.Errorf("until %v", until)
	}
	if _, err := runAccount(t, "", "resetcode", "-data", dir, "nobody"); err == nil {
		t.Error("a code for nobody")
	}
}
