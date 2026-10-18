package clientip

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestHTTPS(t *testing.T) {
	r, err := New(DefaultTrusted)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, remote string
		tls          bool
		proto        []string
		want         bool
	}{
		{"plain http, direct", "203.0.113.7:5000", false, nil, false},
		{"tls, direct", "203.0.113.7:5000", true, nil, true},
		{"tls ignores a header saying http", "203.0.113.7:5000", true, []string{"http"}, true},
		{"untrusted peer forges https", "203.0.113.7:5000", false, []string{"https"}, false},
		{"trusted proxy says https", "172.18.0.2:4000", false, []string{"https"}, true},
		{"trusted proxy, any case and spaces", "127.0.0.1:1", false, []string{" HTTPS "}, true},
		{"trusted proxy says http", "172.18.0.2:4000", false, []string{"http"}, false},
		{"trusted proxy without header", "172.18.0.2:4000", false, nil, false},
		{"trusted proxy, empty header", "172.18.0.2:4000", false, []string{""}, false},
		{"client prepends https, proxy appends http", "10.0.0.1:1", false, []string{"https, http"}, false},
		{"client sends http, proxy appends https", "10.0.0.1:1", false, []string{"http, https"}, true},
		{"several header lines: the last counts", "10.0.0.1:1", false, []string{"https", "http"}, false},
		{"several header lines, last https", "10.0.0.1:1", false, []string{"http", "https"}, true},
		{"trailing comma", "10.0.0.1:1", false, []string{"https,"}, false},
		{"ipv6 loopback proxy", "[::1]:1", false, []string{"https"}, true},
		{"ipv4-mapped trusted peer", "[::ffff:10.0.0.1]:1", false, []string{"https"}, true},
		{"remote without port, trusted", "10.0.0.1", false, []string{"https"}, true},
		{"unparsable remote", "not-an-address", false, []string{"https"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remote
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			} else {
				req.TLS = nil
			}
			for _, v := range tt.proto {
				req.Header.Add("X-Forwarded-Proto", v)
			}
			if got := r.HTTPS(req); got != tt.want {
				t.Errorf("HTTPS = %v, want %v", got, tt.want)
			}
		})
	}
}

// Without trusted proxies, X-Forwarded-Proto is never believed, also from
// loopback.
func TestHTTPSNoTrustedProxies(t *testing.T) {
	r, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Forwarded-Proto", "https")
	if r.HTTPS(req) {
		t.Error("HTTPS = true from an untrusted loopback peer")
	}
}

func FuzzHTTPS(f *testing.F) {
	r, _ := New(DefaultTrusted)
	f.Add("10.0.0.1:1", "https")
	f.Add("[::1]:1", ",,, https")
	f.Fuzz(func(t *testing.T, remote, proto string) {
		req := httptest.NewRequest("GET", "/", nil)
		req.TLS = nil
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-Proto", proto)
		_ = r.HTTPS(req) // must not panic
	})
}

func TestKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":          "203.0.113.7",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":      "2001:db8:1:3::/64",
		"::1":                  "::/64",
		"::ffff:203.0.113.7":   "::ffff:203.0.113.7",
		"not an address":       "not an address",
		"":                     "",
		"fe80::1%eth0":         "fe80::/64",
	} {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}
