// Package mailtest is a small SMTP server in the test process, for tests
// of package mail and of what sends mail: it speaks just enough SMTP for
// net/smtp (EHLO, STARTTLS, AUTH PLAIN, MAIL, RCPT, DATA, QUIT) with a
// certificate made at start, and keeps every mail it accepts.
package mailtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mail is one mail the server accepted.
type Mail struct {
	From, To string
	Data     string // headers and text as sent, with CRLF line ends
	TLS      bool   // it came over an encrypted connection
	Auth     bool   // the client had signed in
}

// Options change how the server behaves.
type Options struct {
	// TLS is "starttls" (plain first, STARTTLS offered) or "tls" (TLS from
	// the first byte); "" is "starttls".
	TLS string
	// NoStartTLS leaves STARTTLS out of the EHLO answer.
	NoStartTLS bool
	// User and Password are the sign-in the server wants; "" takes mail
	// without one.
	User, Password string
	// Reject refuses mail to this address (550).
	Reject string
}

// Server is a running fake SMTP server.
type Server struct {
	Host    string // "127.0.0.1"
	Port    int
	RootCAs *x509.CertPool // trusts the server's certificate

	opts Options
	cert tls.Certificate
	ln   net.Listener

	mu    sync.Mutex // guards mails and fail
	mails []Mail
	fail  int
	wg    sync.WaitGroup
}

// Start runs a server until the test ends.
func Start(t testing.TB, opts Options) *Server {
	t.Helper()
	cert, pool, err := newCert()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Host: "127.0.0.1", RootCAs: pool, opts: opts, cert: cert}
	if s.opts.TLS == "" {
		s.opts.TLS = "starttls"
	}
	if s.opts.TLS == "tls" {
		s.ln, err = tls.Listen("tcp", "127.0.0.1:0", s.tlsConfig())
	} else {
		s.ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	s.Port = s.ln.Addr().(*net.TCPAddr).Port
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() {
		s.ln.Close()
		s.wg.Wait()
	})
	return s
}

// Mails returns the mails accepted so far.
func (s *Server) Mails() []Mail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Mail(nil), s.mails...)
}

// FailNext makes the server refuse the next n mails with a temporary
// error (451).
func (s *Server) FailNext(n int) {
	s.mu.Lock()
	s.fail = n
	s.mu.Unlock()
}

func (s *Server) tlsConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12}
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			s.session(c)
		}()
	}
}

// session talks SMTP on one connection.
func (s *Server) session(c net.Conn) {
	_, encrypted := c.(*tls.Conn)
	tp := textproto.NewConn(c)
	reply := func(code int, msg string) bool { return tp.PrintfLine("%d %s", code, msg) == nil }
	reply(220, "fake ESMTP")
	var m Mail
	auth := false
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			lines := []string{"fake"}
			if !encrypted && s.opts.TLS == "starttls" && !s.opts.NoStartTLS {
				lines = append(lines, "STARTTLS")
			}
			if encrypted || s.opts.NoStartTLS {
				lines = append(lines, "AUTH PLAIN")
			}
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				tp.PrintfLine("250%s%s", sep, l)
			}
		case "STARTTLS":
			reply(220, "go ahead")
			tc := tls.Server(c, s.tlsConfig())
			if tc.Handshake() != nil {
				return
			}
			c, encrypted = tc, true
			tp = textproto.NewConn(tc) // reply writes to the new tp
		case "AUTH":
			mech, resp, _ := strings.Cut(arg, " ")
			raw, err := base64.StdEncoding.DecodeString(resp)
			parts := strings.Split(string(raw), "\x00")
			if strings.ToUpper(mech) != "PLAIN" || err != nil || len(parts) != 3 || parts[1] != s.opts.User || parts[2] != s.opts.Password {
				reply(535, "5.7.8 authentication failed")
				continue
			}
			auth = true
			reply(235, "2.7.0 signed in")
		case "MAIL":
			if s.opts.User != "" && !auth {
				reply(530, "5.7.0 sign in first")
				continue
			}
			m = Mail{From: addr(arg), TLS: encrypted, Auth: auth}
			reply(250, "ok")
		case "RCPT":
			m.To = addr(arg)
			if s.opts.Reject != "" && strings.EqualFold(m.To, s.opts.Reject) {
				reply(550, "5.1.1 <"+m.To+">: recipient address rejected")
				continue
			}
			reply(250, "ok")
		case "DATA":
			reply(354, "go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			m.Data = string(data)
			s.mu.Lock()
			failing := s.fail > 0
			if failing {
				s.fail--
			} else {
				s.mails = append(s.mails, m)
			}
			s.mu.Unlock()
			if failing {
				reply(451, "4.3.0 try again later")
				continue
			}
			reply(250, "2.0.0 queued as "+strconv.Itoa(len(s.Mails())))
		case "RSET", "NOOP":
			reply(250, "ok")
		case "QUIT":
			reply(221, "bye")
			return
		default:
			reply(502, "5.5.2 not implemented")
		}
	}
}

// addr reads the address of "FROM:<a@b>" or "TO:<a@b>".
func addr(arg string) string {
	_, a, _ := strings.Cut(arg, ":")
	a, _, _ = strings.Cut(strings.TrimSpace(a), " ")
	return strings.Trim(a, "<>")
}

// newCert makes a self-signed certificate for 127.0.0.1 and a pool that
// trusts it.
func newCert() (tls.Certificate, *x509.CertPool, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mailtest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool, nil
}
