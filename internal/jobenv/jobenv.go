// Package jobenv builds the environment a job process inherits from the
// worker that starts it. The server and the agent share it so neither leaks
// its own credentials (KAIROS_SECRET, KAIROS_AGENT_TOKEN, database URLs)
// into job shells.
package jobenv

import "strings"

// BaseKeys are the worker variables a job may inherit. Anything else stays
// in the worker process; a job that needs a value sets it in its own env.
var BaseKeys = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"LANG": true, "LANGUAGE": true, "TZ": true, "TMPDIR": true, "TERM": true,
	"DOCKER_HOST": true, "DOCKER_CONFIG": true, "DOCKER_CERT_PATH": true, "DOCKER_TLS_VERIFY": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
}

// Base filters environ down to BaseKeys and LC_*.
func Base(environ []string) []string {
	out := make([]string, 0, len(BaseKeys))
	for _, e := range environ {
		k, _, ok := strings.Cut(e, "=")
		if ok && (BaseKeys[k] || strings.HasPrefix(k, "LC_")) {
			out = append(out, e)
		}
	}
	return out
}

// Merge returns base with overlay applied; an overlay key replaces any
// existing entry of the same name.
func Merge(base []string, overlay map[string]string) []string {
	out := make([]string, 0, len(base)+len(overlay))
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if _, replaced := overlay[k]; !replaced {
			out = append(out, e)
		}
	}
	for k, v := range overlay {
		out = append(out, k+"="+v)
	}
	return out
}
