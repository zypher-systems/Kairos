package agent

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Labels parses KAIROS_AGENT_LABELS as k=v,k2=v2. The server owns an
// agent's labels; these are only reported so a mismatch shows in its log.
func Labels() map[string]string {
	out := map[string]string{}
	raw := os.Getenv("KAIROS_AGENT_LABELS")
	if raw == "" {
		return out
	}
	for _, pair := range splitComma(raw) {
		k, v := splitPair(pair)
		if k != "" {
			out[k] = v
		}
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func splitPair(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func defaultHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func errRequired(name string) error {
	return fmt.Errorf("%s is required", name)
}
