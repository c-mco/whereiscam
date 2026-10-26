package netutil

import (
	"net/http/httptest"
	"testing"
)

func TestTrustsAndClientIP(t *testing.T) {
	tp, err := ParseTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}

	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		trusted bool
		want    string
	}{
		{"untrusted peer ignores headers", "203.0.113.9:5000", map[string]string{"X-Real-IP": "1.1.1.1", "X-Forwarded-For": "2.2.2.2"}, false, "203.0.113.9"},
		{"trusted proxy uses X-Real-IP", "127.0.0.1:5000", map[string]string{"X-Real-IP": "198.51.100.7", "X-Forwarded-For": "6.6.6.6, 198.51.100.7"}, true, "198.51.100.7"},
		{"spoofed leading XFF entry is ignored", "10.1.2.3:5000", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.8"}, true, "198.51.100.8"},
		{"CF-Connecting-IP is not trusted", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "6.6.6.6"}, true, "127.0.0.1"},
		{"garbage X-Real-IP falls back", "127.0.0.1:5000", map[string]string{"X-Real-IP": "not-an-ip"}, true, "127.0.0.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		if got := tp.Trusts(r); got != c.trusted {
			t.Errorf("%s: Trusts = %v, want %v", c.name, got, c.trusted)
		}
		if got := tp.ClientIP(r); got != c.want {
			t.Errorf("%s: ClientIP = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEmptyTrustsNothing(t *testing.T) {
	tp, _ := ParseTrustedProxies(nil)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	if tp.Trusts(r) {
		t.Error("empty proxy list must trust nothing")
	}
	var nilTP *TrustedProxies
	if nilTP.Trusts(r) || nilTP.ClientIP(r) != "127.0.0.1" {
		t.Error("nil TrustedProxies must trust nothing and fall back to RemoteAddr")
	}
}

func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	if _, err := ParseTrustedProxies([]string{"localhost"}); err == nil {
		t.Error("expected error for non-IP entry")
	}
}
