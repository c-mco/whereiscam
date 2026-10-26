package netutil

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// TrustedProxies is an allow-list of reverse proxy addresses whose forwarded
// headers (Remote-User, CF-Connecting-IP, X-Forwarded-For) may be believed.
type TrustedProxies struct {
	nets []*net.IPNet
}

// ParseTrustedProxies accepts bare IPs or CIDRs. An empty list trusts nothing.
func ParseTrustedProxies(entries []string) (*TrustedProxies, error) {
	tp := &TrustedProxies{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.Contains(e, "/") {
			ip := net.ParseIP(e)
			if ip == nil {
				return nil, fmt.Errorf("invalid trusted proxy %q", e)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			e = fmt.Sprintf("%s/%d", ip.String(), bits)
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", e, err)
		}
		tp.nets = append(tp.nets, n)
	}
	return tp, nil
}

// Trusts reports whether the request's direct peer is a trusted proxy.
func (tp *TrustedProxies) Trusts(r *http.Request) bool {
	if tp == nil || len(tp.nets) == 0 {
		return false
	}
	ip := net.ParseIP(remoteHost(r))
	if ip == nil {
		return false
	}
	for _, n := range tp.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP returns the originating client IP, only consulting forwarded
// headers when the direct peer is a trusted proxy. It prefers X-Real-IP, which
// the proxy overwrites with the address it saw, then the last X-Forwarded-For
// hop (appended by the proxy). Earlier XFF entries and CF-Connecting-IP are
// client-controlled and would let callers dodge per-IP rate limits.
func (tp *TrustedProxies) ClientIP(r *http.Request) string {
	if tp.Trusts(r) {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(ip) != nil {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			hops := strings.Split(xff, ",")
			if last := strings.TrimSpace(hops[len(hops)-1]); net.ParseIP(last) != nil {
				return last
			}
		}
	}
	return remoteHost(r)
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
