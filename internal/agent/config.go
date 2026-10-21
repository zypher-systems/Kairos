// Package agent implements the Kairos remote worker.
package agent

import (
	"os"
	"time"
)

// Config is the agent runtime configuration from KAIROS_AGENT_* / env vars.
type Config struct {
	ServerURL string
	Token     string
	Name      string
	Hostname  string
	// HeartbeatInterval paces liveness reports and the cancel/watch feed.
	HeartbeatInterval time.Duration
	// LeaseRenew is only used when the server does not send a lease with
	// the claim; newer servers do, and the agent renews at a third of it.
	LeaseRenew time.Duration
	// DrainTimeout is how long SIGTERM waits for running jobs before they
	// are killed and handed back to the server's queue.
	DrainTimeout time.Duration
	// Concurrency cap for simultaneous runs.
	MaxConcurrent int
	// Docker availability is auto-detected at startup.
}

// Load reads agent config from the environment.
func Load() (*Config, error) {
	cfg := &Config{
		ServerURL:         getenv("KAIROS_SERVER_URL", ""),
		Token:             os.Getenv("KAIROS_AGENT_TOKEN"),
		Name:              getenv("KAIROS_AGENT_NAME", ""),
		Hostname:          getenv("KAIROS_AGENT_HOSTNAME", defaultHostname()),
		HeartbeatInterval: getenvDuration("KAIROS_AGENT_HEARTBEAT", 15*time.Second),
		LeaseRenew:        getenvDuration("KAIROS_AGENT_LEASE_RENEW", 10*time.Second),
		DrainTimeout:      getenvDuration("KAIROS_AGENT_DRAIN", 30*time.Second),
		MaxConcurrent:     getenvInt("KAIROS_AGENT_MAX_CONCURRENT", 2),
	}
	if cfg.ServerURL == "" {
		return nil, errRequired("KAIROS_SERVER_URL")
	}
	if cfg.Token == "" {
		return nil, errRequired("KAIROS_AGENT_TOKEN")
	}
	if cfg.Name == "" {
		cfg.Name = cfg.Hostname
	}
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 1
	}
	return cfg, nil
}
