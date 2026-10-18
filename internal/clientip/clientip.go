// Package clientip finds the address of the client behind a request, also
// when the engine runs behind a reverse proxy such as Caddy.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultTrusted are the proxy addresses trusted out of the box: loopback and
// private networks, where Caddy runs in the default Docker Compose setup.
const DefaultTrusted = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

// Resolver reads X-Forwarded-For only from trusted proxies.
type Resolver struct {
	trusted []netip.Prefix
}

// New parses a comma-separated list of CIDR prefixes. An empty list trusts
// no proxy, so the TCP peer address is always used.
func New(trusted string) (Resolver, error) {
	var r Resolver
	for _, s := range strings.Split(trusted, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return Resolver{}, fmt.Errorf("clientip: trusted proxy %q: %w", s, err)
		}
		r.trusted = append(r.trusted, p.Masked())
	}
	return r, nil
}

func (r Resolver) isTrusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// IP returns the client's address. Behind trusted proxies it is the
// right-most X-Forwarded-For entry that is not itself a trusted proxy;
// entries further left could be made up by the client.
func (r Resolver) IP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if !r.isTrusted(peer) {
		return peer.Unmap().String()
	}
	client := peer
	hops := strings.Split(strings.Join(req.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break // a malformed entry ends the trustworthy part of the chain
		}
		client = a
		if !r.isTrusted(a) {
			break
		}
	}
	return client.Unmap().String()
}

// HTTPS reports whether the client reached the server over HTTPS: the
// request came in over TLS, or a trusted proxy says so in
// X-Forwarded-Proto. From any other peer the header is ignored.
func (r Resolver) HTTPS(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !r.isTrusted(peer) {
		return false
	}
	protos := strings.Split(strings.Join(req.Header.Values("X-Forwarded-Proto"), ","), ",")
	return strings.EqualFold(strings.TrimSpace(protos[len(protos)-1]), "https")
}

// Key is what per-client limits count by: the address, or for IPv6 its
// /64, which one home or school network usually gets as a whole. Counting
// whole IPv6 addresses would give every client 2^64 fresh limits.
func Key(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() || a.Is4In6() {
		return ip
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}
