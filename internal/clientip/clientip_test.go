package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestIP(t *testing.T) {
	r, err := New(DefaultTrusted)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"direct client", "203.0.113.7:5000", nil, "203.0.113.7"},
		{"direct client ignores forged header", "203.0.113.7:5000", []string{"1.2.3.4"}, "203.0.113.7"},
		{"behind caddy", "172.18.0.2:4000", []string{"198.51.100.9"}, "198.51.100.9"},
		{"client prepends a fake hop", "172.18.0.2:4000", []string{"1.2.3.4, 198.51.100.9"}, "198.51.100.9"},
		{"two trusted proxies", "127.0.0.1:1", []string{"198.51.100.9, 10.0.0.5"}, "198.51.100.9"},
		{"several header lines", "10.0.0.1:1", []string{"1.1.1.1", "198.51.100.9"}, "198.51.100.9"},
		{"proxy without header", "172.18.0.2:4000", nil, "172.18.0.2"},
		{"malformed entry stops the walk", "10.0.0.1:1", []string{"198.51.100.9, junk"}, "10.0.0.1"},
		{"ipv6 client", "[2001:db8::1]:443", nil, "2001:db8::1"},
		{"ipv4-mapped peer", "[::ffff:203.0.113.7]:1", nil, "203.0.113.7"},
		{"remote without port", "203.0.113.7", nil, "203.0.113.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remote
			for _, v := range tt.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := r.IP(req); got != tt.want {
				t.Errorf("IP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNoTrustedProxies(t *testing.T) {
	r, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	if got := r.IP(req); got != "127.0.0.1" {
		t.Errorf("IP = %q, want the peer", got)
	}
}

func TestNewRejectsBadPrefix(t *testing.T) {
	if _, err := New("10.0.0.0/8, not-a-cidr"); err == nil {
		t.Error("want error")
	}
}

func FuzzIP(f *testing.F) {
	r, _ := New(DefaultTrusted)
	f.Add("10.0.0.1:1", "1.2.3.4, 5.6.7.8")
	f.Add("[::1]:1", ",,, ::ffff:1.2.3.4")
	f.Fuzz(func(t *testing.T, remote, xff string) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-For", xff)
		_ = r.IP(req) // must not panic
	})
}
