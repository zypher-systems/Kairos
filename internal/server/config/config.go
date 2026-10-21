package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration, sourced from KAIROS_* env vars.
type Config struct {
	HTTPAddr    string
	DBURL       string
	DataDir     string
	Secret      string
	Development bool
	HTTPS       bool
	// LogLevel is debug|info|warn|error; LogFormat is text|json.
	LogLevel  string
	LogFormat string
	PublicURL string
	// MetricsToken, when set, is the bearer token /metrics requires
	// (KAIROS_METRICS_TOKEN). Empty leaves /metrics open, as before.
	MetricsToken string
	// DBMaxConns caps the connection pool (KAIROS_DB_MAX_CONNS). 0 lets
	// pgx choose (max(4, NumCPU)), which is too small for a busy server.
	DBMaxConns int
	// TrustedProxies are the reverse proxies whose X-Forwarded-For /
	// X-Real-IP headers are believed (KAIROS_TRUSTED_PROXIES, comma-separated
	// IPs or CIDRs). Empty means no forwarding header is trusted.
	TrustedProxies []*net.IPNet

	// Scheduler
	SchedulerTick     time.Duration
	QueueLease        time.Duration
	HeartbeatTimeout  time.Duration
	RetentionInterval time.Duration
	// DispatcherConcurrency caps local and SSH runs executing on this
	// replica at once (KAIROS_DISPATCHER_CONCURRENCY).
	DispatcherConcurrency int
	// ShutdownGrace is how long SIGTERM waits for in-flight local and SSH
	// runs before they are killed and returned to the queue
	// (KAIROS_SHUTDOWN_GRACE).
	ShutdownGrace time.Duration
}

func Load() (*Config, error) {
	c := &Config{
		HTTPAddr:          getenv("KAIROS_HTTP_ADDR", ":8080"),
		DBURL:             getenv("KAIROS_DB_URL", "postgres://kairos:kairos@localhost:5432/kairos?sslmode=disable"),
		DataDir:           getenv("KAIROS_DATA_DIR", "/var/lib/kairos"),
		Secret:            os.Getenv("KAIROS_SECRET"),
		Development:       getenv("KAIROS_DEVELOPMENT", "false") == "true",
		HTTPS:             getenv("KAIROS_HTTPS", "false") == "true",
		LogLevel:          strings.ToLower(getenv("KAIROS_LOG_LEVEL", "info")),
		LogFormat:         strings.ToLower(getenv("KAIROS_LOG_FORMAT", "text")),
		PublicURL:         os.Getenv("KAIROS_PUBLIC_URL"),
		MetricsToken:      strings.TrimSpace(os.Getenv("KAIROS_METRICS_TOKEN")),
		DBMaxConns:        getenvInt("KAIROS_DB_MAX_CONNS", 16),
		SchedulerTick:     time.Second,
		QueueLease:        getenvDuration("KAIROS_QUEUE_LEASE", 30*time.Second),
		HeartbeatTimeout:  getenvDuration("KAIROS_HEARTBEAT_TIMEOUT", 90*time.Second),
		RetentionInterval: time.Hour,

		DispatcherConcurrency: getenvInt("KAIROS_DISPATCHER_CONCURRENCY", 8),
		ShutdownGrace:         getenvDuration("KAIROS_SHUTDOWN_GRACE", 30*time.Second),
	}
	proxies, err := parseTrustedProxies(os.Getenv("KAIROS_TRUSTED_PROXIES"))
	if err != nil {
		return nil, err
	}
	c.TrustedProxies = proxies
	if len(parseErrors) > 0 {
		// A setting that does not parse is a typo in production config;
		// running on the default would hide it.
		errs := parseErrors
		parseErrors = nil
		return nil, errors.New(strings.Join(errs, "; "))
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	if c.DBURL == "" {
		return fmt.Errorf("KAIROS_DB_URL is required")
	}
	if err := ValidateSecret(c.Secret); err != nil {
		return err
	}
	if c.SchedulerTick < 100*time.Millisecond {
		return fmt.Errorf("KAIROS tick interval too small")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("KAIROS_LOG_LEVEL must be debug, info, warn or error")
	}
	switch c.LogFormat {
	case "text", "json":
	default:
		return fmt.Errorf("KAIROS_LOG_FORMAT must be text or json")
	}
	if c.DBMaxConns < 0 || c.DBMaxConns > 1000 {
		return fmt.Errorf("KAIROS_DB_MAX_CONNS must be between 0 and 1000")
	}
	if c.HeartbeatTimeout < 2*c.QueueLease {
		return fmt.Errorf("KAIROS_HEARTBEAT_TIMEOUT (%s) must be at least twice KAIROS_QUEUE_LEASE (%s)", c.HeartbeatTimeout, c.QueueLease)
	}
	if c.QueueLease < 5*time.Second {
		return fmt.Errorf("KAIROS_QUEUE_LEASE must be at least 5s")
	}
	if c.DispatcherConcurrency < 1 || c.DispatcherConcurrency > 256 {
		return fmt.Errorf("KAIROS_DISPATCHER_CONCURRENCY must be between 1 and 256")
	}
	if c.ShutdownGrace < 0 || c.ShutdownGrace > time.Hour {
		return fmt.Errorf("KAIROS_SHUTDOWN_GRACE must be between 0 and 1h")
	}
	return nil
}

// OriginAllowed reports whether a browser Origin is same-site for this
// process. It matches r.Host, or the host of KAIROS_PUBLIC_URL when that is
// set. Reverse proxies often rewrite Host to the backend (localhost:8080)
// while the browser Origin is the public DNS name.
func (c *Config) OriginAllowed(origin, reqHost string) bool {
	if c == nil {
		return OriginAllowed(origin, reqHost, "")
	}
	return OriginAllowed(origin, reqHost, c.PublicURL)
}

// OriginAllowed compares a browser Origin to the request Host and optional
// public URL. Empty or malformed origins are not allowed.
func OriginAllowed(origin, reqHost, publicURL string) bool {
	u, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || u.Host == "" {
		return false
	}
	oh := canonicalHost(u.Host)
	if oh != "" && strings.EqualFold(oh, canonicalHost(reqHost)) {
		return true
	}
	if pub := publicURLHost(publicURL); pub != "" && strings.EqualFold(oh, pub) {
		return true
	}
	return false
}

func publicURLHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return canonicalHost(u.Host)
}

func canonicalHost(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		return strings.ToLower(h)
	}
	if port == "80" || port == "443" {
		return strings.ToLower(host)
	}
	return strings.ToLower(h)
}

// DefaultSecret is the compose / example placeholder. It must never be used
// in a running process — empty or this value stores job secrets in plaintext.
const DefaultSecret = "change-me-please"

// ValidateSecret rejects empty and placeholder master secrets.
func ValidateSecret(secret string) error {
	if secret == "" || secret == DefaultSecret {
		return fmt.Errorf("KAIROS_SECRET must be set to a non-default value")
	}
	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseErrors collects settings that did not parse; Load turns them into
// one error so a typo is reported instead of silently defaulted.
var parseErrors []string

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			parseErrors = append(parseErrors, key+": "+strconv.Quote(v)+" is not an integer")
			return def
		}
		return n
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			parseErrors = append(parseErrors, key+": "+strconv.Quote(v)+" is not a duration (use 30s, 5m, 1h)")
			return def
		}
		return d
	}
	return def
}

// SlogLevel maps LogLevel to slog.
func (c *Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// parseTrustedProxies reads "10.0.0.1, 172.16.0.0/12" into networks.
func parseTrustedProxies(raw string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			ip := net.ParseIP(f)
			if ip == nil {
				return nil, fmt.Errorf("KAIROS_TRUSTED_PROXIES: %q is not an IP or CIDR", f)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(f)
		if err != nil {
			return nil, fmt.Errorf("KAIROS_TRUSTED_PROXIES: %w", err)
		}
		out = append(out, n)
	}
	return out, nil
}

// TrustsProxy reports whether ip is a configured reverse proxy.
func (c *Config) TrustsProxy(ip net.IP) bool {
	for _, n := range c.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
