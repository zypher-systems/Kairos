package notify

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// ErrSSRF is returned when a webhook or SMTP target is not allowed.
var ErrSSRF = fmt.Errorf("destination is not allowed")

// CheckDialTarget rejects loopback, link-local, and RFC1918 destinations unless
// the host is listed in KAIROS_NOTIFY_ALLOW_HOSTS (comma-separated).
func CheckDialTarget(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrSSRF)
	}
	if allowedHost(host) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return fmt.Errorf("%w: %s", ErrSSRF, host)
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: no addresses for %s", ErrSSRF, host)
	}
	for _, ip := range addrs {
		if blockedIP(ip) {
			return fmt.Errorf("%w: %s resolves to %s", ErrSSRF, host, ip)
		}
	}
	return nil
}

// CheckHTTPURL applies CheckDialTarget to the URL host.
func CheckHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: invalid url", ErrSSRF)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %s", ErrSSRF, u.Scheme)
	}
	host := u.Hostname()
	return CheckDialTarget(host)
}

func allowedHost(host string) bool {
	raw := os.Getenv("KAIROS_NOTIFY_ALLOW_HOSTS")
	if raw == "" {
		return false
	}
	host = strings.ToLower(host)
	for _, h := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

// extraBlocked covers special-use ranges the net.IP helpers miss.
var extraBlocked = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8",     // "this network"
		"100.64.0.0/10", // carrier-grade NAT
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmarking
		"240.0.0.0/4",   // reserved
		"64:ff9b::/96",  // NAT64: embeds any IPv4, including private ones
		"64:ff9b:1::/48",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

func blockedIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4 // IPv4-mapped IPv6 is judged as IPv4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, n := range extraBlocked {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// safeDialer re-checks the address actually being connected to, after DNS,
// so a name that resolves differently at send time (DNS rebinding) cannot
// reach a blocked range. Allow-listed hosts skip the check.
func safeDialer(host string, timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	if allowedHost(host) {
		return d
	}
	d.Control = func(_, address string, _ syscall.RawConn) error {
		h, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(h)
		if ip == nil || blockedIP(ip) {
			return fmt.Errorf("%w: %s", ErrSSRF, h)
		}
		return nil
	}
	return d
}

// newSafeHTTPClient is the client for webhooks, Slack and Discord: every
// connection goes through safeDialer, redirects are not followed (a 302
// to an internal address would otherwise bypass the check), and proxy
// environment variables are ignored.
func newSafeHTTPClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			return safeDialer(host, 10*time.Second).DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// headerValue strips CR and LF so a job name or address cannot add headers.
func headerValue(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
