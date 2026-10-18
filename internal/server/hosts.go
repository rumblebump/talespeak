package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
)

// hostName is a DNS name: labels of letters, digits and hyphens.
var hostName = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ParseHosts reads a comma-separated list of host names, such as the
// value of TALESPEAK_HOSTS. Empty means every host is served.
func ParseHosts(s string) ([]string, error) {
	var hosts []string
	for _, h := range strings.Split(s, ",") {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h == "" {
			continue
		}
		if len(h) > 253 || !hostName.MatchString(h) {
			return nil, fmt.Errorf("host %q: want a DNS name like play.example.org, without scheme or port", h)
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

// checkHost answers 421 to a request whose Host is not one of hosts, so a
// page on another domain that a rebinding DNS server points at the engine
// gets nothing (the browser sends that domain as Host). IP addresses and
// localhost are always served, for health checks and local use. With no
// hosts, every request is served.
func checkHost(hosts []string, next http.Handler) http.Handler {
	if len(hosts) == 0 {
		return next
	}
	allowed := make(map[string]bool, len(hosts)+1)
	for _, h := range hosts {
		allowed[h] = true
	}
	allowed["localhost"] = true
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err != nil && !allowed[host] {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
