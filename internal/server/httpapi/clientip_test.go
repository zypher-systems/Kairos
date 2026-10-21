package httpapi

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/config"
)

func seenIP(t *testing.T, a *API, remote string, hdr map[string]string) string {
	t.Helper()
	var got string
	h := a.trustedRealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = clientIP(r) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestForwardedHeadersIgnoredWithoutTrustedProxy(t *testing.T) {
	a := &API{cfg: &config.Config{}}
	if got := seenIP(t, a, "203.0.113.9:5555", map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "5.6.7.8"}); got != "203.0.113.9" {
		t.Fatalf("spoofed header accepted: %s", got)
	}
}

func TestForwardedHeadersFromTrustedProxy(t *testing.T) {
	_, n, _ := net.ParseCIDR("10.0.0.0/8")
	a := &API{cfg: &config.Config{TrustedProxies: []*net.IPNet{n}}}
	// Client-supplied left-most hops are ignored; the right-most untrusted hop wins.
	if got := seenIP(t, a, "10.0.0.2:443", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7, 10.0.0.3"}); got != "198.51.100.7" {
		t.Fatalf("want 198.51.100.7, got %s", got)
	}
	if got := seenIP(t, a, "10.0.0.2:443", map[string]string{"X-Real-IP": "198.51.100.8"}); got != "198.51.100.8" {
		t.Fatalf("want X-Real-IP, got %s", got)
	}
	// A request that did not come through the proxy keeps its own address.
	if got := seenIP(t, a, "203.0.113.9:5555", map[string]string{"X-Forwarded-For": "1.2.3.4"}); got != "203.0.113.9" {
		t.Fatalf("untrusted peer header accepted: %s", got)
	}
}
