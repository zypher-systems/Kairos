package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// APIToken is a long-lived bearer credential. The secret is shown once.
type APIToken struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Token       string     `json:"token,omitempty"`
	RoleCeiling *string    `json:"role_ceiling,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	LastUsed    *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ErrTokenExpired is returned for a token past its expires_at.
var ErrTokenExpired = errors.New("api token expired")

// CreateAPIToken issues a new token for adminID. The plaintext is returned
// once. expiresAt nil keeps the token until it is deleted.
func (s *Store) CreateAPIToken(ctx context.Context, adminID uuid.UUID, name string, ceiling *string, expiresAt *time.Time) (APIToken, error) {
	if name == "" {
		name = "api"
	}
	if ceiling != nil && *ceiling != "" {
		norm, err := NormalizeRole(*ceiling)
		if err != nil {
			return APIToken{}, err
		}
		ceiling = &norm
	} else {
		ceiling = nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return APIToken{}, err
	}
	secret := "kat_" + base64.RawURLEncoding.EncodeToString(raw)
	prefix := secret[:12]
	var tok APIToken
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (admin_id, name, token_hash, prefix, role_ceiling, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, prefix, role_ceiling, expires_at, last_used_at, created_at`,
		adminID, name, HashSessionToken(secret), prefix, ceiling, expiresAt,
	).Scan(&tok.ID, &tok.Name, &tok.Prefix, &tok.RoleCeiling, &tok.ExpiresAt, &tok.LastUsed, &tok.CreatedAt)
	tok.Token = secret
	return tok, err
}

// ListAPITokens lists tokens for an admin (secrets never returned).
func (s *Store) ListAPITokens(ctx context.Context, adminID uuid.UUID) ([]APIToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, prefix, role_ceiling, expires_at, last_used_at, created_at
		FROM api_tokens WHERE admin_id = $1 ORDER BY created_at DESC`, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.RoleCeiling, &t.ExpiresAt, &t.LastUsed, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken removes one token owned by adminID.
func (s *Store) DeleteAPIToken(ctx context.Context, adminID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND admin_id = $2`, id, adminID)
	return err
}

// APITokenAdmin resolves a bearer API token to an identity. Effective role is
// min(owner role, optional ceiling). Disabled owners are rejected.
func (s *Store) APITokenAdmin(ctx context.Context, token string) (Identity, error) {
	var adminID uuid.UUID
	var ceiling *string
	var expiresAt *time.Time
	err := s.pool.QueryRow(ctx, `
		UPDATE api_tokens SET last_used_at = now()
		WHERE token_hash = $1
		RETURNING admin_id, role_ceiling, expires_at`, HashSessionToken(token)).Scan(&adminID, &ceiling, &expiresAt)
	if err != nil {
		return Identity{}, err
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return Identity{}, ErrTokenExpired
	}
	var ident Identity
	var enabled bool
	err = s.pool.QueryRow(ctx, `
		SELECT id, username, role, enabled FROM admins WHERE id = $1`, adminID,
	).Scan(&ident.ID, &ident.Username, &ident.Role, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, err
	}
	if err != nil {
		return Identity{}, err
	}
	if !enabled {
		return Identity{}, ErrAccountDisabled
	}
	if ceiling != nil && *ceiling != "" {
		ident.Role = MinRole(ident.Role, *ceiling)
	}
	ident.ViaToken = true
	return ident, nil
}
