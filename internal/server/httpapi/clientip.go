package httpapi

import (
	"net"
	"net/http"
	"strings"
)

// trustedRealIP replaces chi's RealIP. Forwarding headers are honoured only
// when the direct peer is a configured trusted proxy
// (KAIROS_TRUSTED_PROXIES); otherwise any client could pick its own address
// and slip past the login limiter or forge audit-log IPs.
func (a *API) trustedRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := a.forwardedClientIP(r); ip != "" {
			r.RemoteAddr = net.JoinHostPort(ip, "0")
		}
		next.ServeHTTP(w, r)
	})
}

// forwardedClientIP returns the client address claimed by a trusted proxy,
// or "" to keep the connection's own address.
func (a *API) forwardedClientIP(r *http.Request) string {
	if a.cfg == nil || len(a.cfg.TrustedProxies) == 0 {
		return ""
	}
	peer := net.ParseIP(clientIP(r))
	if peer == nil || !a.cfg.TrustsProxy(peer) {
		return ""
	}
	// X-Forwarded-For is "client, proxy1, proxy2". Walk from the right and
	// take the first hop that is not one of our proxies: everything left of
	// it was written by the client and cannot be trusted.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(hops[i]))
			if ip == nil {
				return ""
			}
			if !a.cfg.TrustsProxy(ip) {
				return ip.String()
			}
		}
		return ""
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	return ""
}
