package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Settings is the global application configuration row (single row, key 'app').
// Secrets are never stored here in the clear.
type Settings struct {
	DefaultTimezone        string     `json:"default_timezone"`
	RetentionRuns          int        `json:"retention_runs"`
	RetentionDays          int        `json:"retention_days"`
	LibraryMaxAgeDays      int        `json:"library_max_age_days"` // 0 = keep files forever
	GitSyncEnabled         bool       `json:"git_sync_enabled"`
	GitSyncURL             string     `json:"git_sync_url"`
	GitSyncRef             string     `json:"git_sync_ref"`
	GitSyncPath            string     `json:"git_sync_path"`
	GitSyncIntervalSeconds int        `json:"git_sync_interval_seconds"`
	GitSyncToken           string     `json:"git_sync_token,omitempty"`
	GitSyncTokenEnc        string     `json:"git_sync_token_enc,omitempty"`
	GitSyncHasToken        bool       `json:"git_sync_has_token"`
	GitSyncLastAt          *time.Time `json:"git_sync_last_at,omitempty"`
	GitSyncLastSHA         string     `json:"git_sync_last_sha,omitempty"`
	GitSyncLastError       string     `json:"git_sync_last_error,omitempty"`
	GitSyncLastSummary     string     `json:"git_sync_last_summary,omitempty"`
	SSOEnabled             bool       `json:"sso_enabled"`
	SSOIssuer              string     `json:"sso_issuer"`
	SSOClientID            string     `json:"sso_client_id"`
	SSOClientSecret        string     `json:"sso_client_secret,omitempty"`
	SSOClientSecretEnc     string     `json:"sso_client_secret_enc,omitempty"`
	SSORedirectURL         string     `json:"sso_redirect_url"`
	SSOHasSecret           bool       `json:"sso_has_secret"`
}

// DefaultSettings is applied on first boot.
func DefaultSettings() Settings {
	return Settings{
		DefaultTimezone:   "UTC",
		RetentionRuns:     100,
		RetentionDays:     30,
		LibraryMaxAgeDays: 90,
	}
}

// GetSettings returns stored settings merged over defaults.
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	cfg := DefaultSettings()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'app'`).Scan(&raw)
	if err == pgx.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.DefaultTimezone == "" {
		cfg.DefaultTimezone = "UTC"
	}
	if cfg.RetentionRuns <= 0 {
		cfg.RetentionRuns = 100
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 30
	}
	if cfg.LibraryMaxAgeDays < 0 {
		cfg.LibraryMaxAgeDays = 0
	}
	if cfg.GitSyncRef == "" {
		cfg.GitSyncRef = "main"
	}
	if cfg.GitSyncPath == "" {
		cfg.GitSyncPath = "."
	}
	if cfg.GitSyncIntervalSeconds <= 0 {
		cfg.GitSyncIntervalSeconds = 300
	}
	if cfg.GitSyncTokenEnc != "" {
		cfg.GitSyncHasToken = true
	}
	if cfg.SSOClientSecretEnc != "" {
		cfg.SSOHasSecret = true
	}
	cfg.GitSyncToken = ""
	cfg.GitSyncTokenEnc = ""
	cfg.SSOClientSecret = ""
	cfg.SSOClientSecretEnc = ""
	return cfg, nil
}

// RevealGitSyncToken decrypts the stored PAT, if any.
func (s *Store) RevealGitSyncToken(ctx context.Context) (string, error) {
	cfg, err := s.getSettingsRaw(ctx)
	if err != nil {
		return "", err
	}
	if cfg.GitSyncTokenEnc == "" || s.box == nil {
		return "", nil
	}
	raw, err := s.box.Open(cfg.GitSyncTokenEnc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (s *Store) getSettingsRaw(ctx context.Context) (Settings, error) {
	cfg := DefaultSettings()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'app'`).Scan(&raw)
	if err == pgx.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// SettingsForWrite returns settings including sealed git token and SSO secret.
func (s *Store) SettingsForWrite(ctx context.Context) (Settings, error) {
	return s.getSettingsRaw(ctx)
}

// RevealSSOClientSecret decrypts the stored OIDC client secret, if any.
func (s *Store) RevealSSOClientSecret(ctx context.Context) (string, error) {
	cfg, err := s.getSettingsRaw(ctx)
	if err != nil {
		return "", err
	}
	if cfg.SSOClientSecretEnc == "" || s.box == nil {
		return "", nil
	}
	raw, err := s.box.Open(cfg.SSOClientSecretEnc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// SealSSOClientSecret encrypts a client secret for storage. Empty input is a no-op.
func (s *Store) SealSSOClientSecret(plain string) (string, error) {
	if plain == "" || s.box == nil {
		return plain, nil
	}
	return s.box.Seal([]byte(plain))
}

// SSOConfigured reports whether OIDC can be offered (enabled + issuer + client + secret + redirect).
func (cfg Settings) SSOConfigured() bool {
	return cfg.SSOEnabled &&
		strings.TrimSpace(cfg.SSOIssuer) != "" &&
		strings.TrimSpace(cfg.SSOClientID) != "" &&
		(cfg.SSOHasSecret || cfg.SSOClientSecretEnc != "") &&
		strings.TrimSpace(cfg.SSORedirectURL) != ""
}

// PatchGitSyncStatus writes last-sync metadata without touching other fields.
func (s *Store) PatchGitSyncStatus(ctx context.Context, sha, errMsg, summary string) error {
	cfg, err := s.getSettingsRaw(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	cfg.GitSyncLastAt = &now
	cfg.GitSyncLastSHA = sha
	cfg.GitSyncLastError = errMsg
	cfg.GitSyncLastSummary = summary
	cfg.GitSyncToken = ""
	return s.SaveSettings(ctx, cfg)
}

// SealGitSyncToken encrypts a PAT for storage. Empty input is a no-op.
func (s *Store) SealGitSyncToken(plain string) (string, error) {
	if plain == "" || s.box == nil {
		return plain, nil
	}
	return s.box.Seal([]byte(plain))
}

// SaveSettings upserts the application settings row.
func (s *Store) SaveSettings(ctx context.Context, cfg Settings) error {
	cfg.GitSyncToken = ""
	cfg.GitSyncHasToken = cfg.GitSyncTokenEnc != ""
	cfg.SSOClientSecret = ""
	cfg.SSOHasSecret = cfg.SSOClientSecretEnc != ""
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ('app', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		raw,
	)
	return err
}

// EnsureBootstrapped creates the default settings row if absent.
func (s *Store) EnsureBootstrapped(ctx context.Context) error {
	raw, err := json.Marshal(DefaultSettings())
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ('app', $1) ON CONFLICT (key) DO NOTHING`,
		raw,
	)
	return err
}
