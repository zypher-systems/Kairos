package config

import (
	"strings"
	"testing"
)

func TestValidateSecret(t *testing.T) {
	if err := ValidateSecret(""); err == nil {
		t.Fatal("empty secret must fail")
	}
	if err := ValidateSecret(DefaultSecret); err == nil {
		t.Fatal("placeholder secret must fail")
	}
	if err := ValidateSecret("a-real-long-secret"); err != nil {
		t.Fatalf("valid secret rejected: %v", err)
	}
}

func TestLoadRejectsDefaultSecret(t *testing.T) {
	t.Setenv("KAIROS_DB_URL", "postgres://kairos:kairos@localhost:5432/kairos?sslmode=disable")
	t.Setenv("KAIROS_SECRET", DefaultSecret)
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted default secret")
	}
}

func TestOriginAllowedPublicURL(t *testing.T) {
	if !OriginAllowed("https://kairos.example.com", "127.0.0.1:8080", "https://kairos.example.com") {
		t.Fatal("proxy backend Host must accept PUBLIC_URL origin")
	}
	if OriginAllowed("https://evil.example", "127.0.0.1:8080", "https://kairos.example.com") {
		t.Fatal("foreign origin must fail")
	}
	if !OriginAllowed("http://kairos.local:8080", "kairos.local:8080", "") {
		t.Fatal("same host without PUBLIC_URL")
	}
	if OriginAllowed("https://kairos.example.com", "127.0.0.1:8080", "") {
		t.Fatal("public origin without PUBLIC_URL must fail")
	}
}

func TestLoadRejectsUnparsableSettings(t *testing.T) {
	t.Setenv("KAIROS_SECRET", "a-real-secret-value-for-the-test")
	t.Setenv("KAIROS_QUEUE_LEASE", "30") // no unit
	t.Setenv("KAIROS_DB_MAX_CONNS", "lots")
	_, err := Load()
	if err == nil {
		t.Fatal("unparsable settings must fail boot")
	}
	msg := err.Error()
	if !strings.Contains(msg, "KAIROS_QUEUE_LEASE") || !strings.Contains(msg, "KAIROS_DB_MAX_CONNS") {
		t.Fatalf("error must name every bad setting: %s", msg)
	}
}

func TestLoadValidatesRelationships(t *testing.T) {
	t.Setenv("KAIROS_SECRET", "a-real-secret-value-for-the-test")
	t.Setenv("KAIROS_QUEUE_LEASE", "60s")
	t.Setenv("KAIROS_HEARTBEAT_TIMEOUT", "90s")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "KAIROS_HEARTBEAT_TIMEOUT") {
		t.Fatalf("heartbeat timeout below twice the lease must be rejected: %v", err)
	}
	t.Setenv("KAIROS_HEARTBEAT_TIMEOUT", "120s")
	t.Setenv("KAIROS_LOG_FORMAT", "JSON")
	t.Setenv("KAIROS_LOG_LEVEL", "Warn")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFormat != "json" || cfg.LogLevel != "warn" {
		t.Fatalf("log settings are case-insensitive: %+v", cfg)
	}
	t.Setenv("KAIROS_LOG_LEVEL", "loud")
	if _, err := Load(); err == nil {
		t.Fatal("unknown log level must be rejected")
	}
}
