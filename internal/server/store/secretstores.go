package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SecretStore is an external credential backend. Token is sealed at rest
// and masked on API reads.
type SecretStore struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // vault
	Address   string    `json:"address"`
	Mount     string    `json:"mount"`
	Namespace string    `json:"namespace,omitempty"`
	Token     string    `json:"token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type secretStoreConfig struct {
	Address   string `json:"address"`
	Mount     string `json:"mount"`
	Namespace string `json:"namespace,omitempty"`
}

// Validate checks store invariants, applying defaults first.
func (s *SecretStore) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return errors.New("name is required")
	}
	if s.Type == "" {
		s.Type = "vault"
	}
	if s.Type != "vault" {
		return errors.New("type must be vault")
	}
	s.Address = strings.TrimRight(strings.TrimSpace(s.Address), "/")
	if s.Address == "" {
		return errors.New("address is required")
	}
	u, err := url.Parse(s.Address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("address must be an http or https URL")
	}
	if s.Mount == "" {
		s.Mount = "secret"
	}
	s.Mount = strings.Trim(s.Mount, "/")
	if s.Mount == "" {
		return errors.New("mount is required")
	}
	s.Namespace = strings.TrimSpace(s.Namespace)
	return nil
}

func (s *Store) sealStoreToken(token string) (string, error) {
	if token == "" || token == SecretMask {
		return "", nil
	}
	if s.box == nil {
		return token, nil
	}
	return s.box.Seal([]byte(token))
}

func (s *Store) revealStoreToken(cipher string) string {
	if cipher == "" || cipher == SecretMask {
		return ""
	}
	if s.box == nil {
		return cipher
	}
	raw, err := s.box.Open(cipher)
	if err != nil {
		return ""
	}
	return string(raw)
}

// MaskSecretStore redacts the token for API responses.
func MaskSecretStore(st *SecretStore) {
	if st.Token != "" && st.Token != SecretMask {
		st.Token = SecretMask
	}
}

const secretStoreCols = `id, name, type, config, token_cipher, created_at, updated_at`

func scanSecretStore(row pgx.Row) (SecretStore, error) {
	var st SecretStore
	var raw []byte
	var cipher string
	err := row.Scan(&st.ID, &st.Name, &st.Type, &raw, &cipher, &st.CreatedAt, &st.UpdatedAt)
	if err != nil {
		return st, err
	}
	var cfg secretStoreConfig
	_ = json.Unmarshal(raw, &cfg)
	st.Address = cfg.Address
	st.Mount = cfg.Mount
	st.Namespace = cfg.Namespace
	st.Token = cipher
	return st, nil
}

func marshalStoreConfig(st SecretStore) ([]byte, error) {
	return json.Marshal(secretStoreConfig{
		Address:   st.Address,
		Mount:     st.Mount,
		Namespace: st.Namespace,
	})
}

func (s *Store) CreateSecretStore(ctx context.Context, st *SecretStore) error {
	if err := st.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(st.Token) == "" || st.Token == SecretMask {
		return errors.New("token is required")
	}
	cipher, err := s.sealStoreToken(st.Token)
	if err != nil {
		return err
	}
	raw, err := marshalStoreConfig(*st)
	if err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `
		INSERT INTO secret_stores (name, type, config, token_cipher)
		VALUES ($1,$2,$3,$4)
		RETURNING id, created_at, updated_at`,
		st.Name, st.Type, raw, cipher,
	).Scan(&st.ID, &st.CreatedAt, &st.UpdatedAt)
}

func (s *Store) GetSecretStore(ctx context.Context, id uuid.UUID) (SecretStore, error) {
	return scanSecretStore(s.pool.QueryRow(ctx, `SELECT `+secretStoreCols+` FROM secret_stores WHERE id = $1`, id))
}

// GetSecretStoreForDelivery loads a store with the Vault token revealed.
func (s *Store) GetSecretStoreForDelivery(ctx context.Context, id uuid.UUID) (SecretStore, error) {
	st, err := s.GetSecretStore(ctx, id)
	if err != nil {
		return st, err
	}
	st.Token = s.revealStoreToken(st.Token)
	return st, nil
}

func (s *Store) ListSecretStores(ctx context.Context) ([]SecretStore, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+secretStoreCols+` FROM secret_stores ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretStore
	for rows.Next() {
		st, err := scanSecretStore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSecretStore(ctx context.Context, st *SecretStore) error {
	if err := st.Validate(); err != nil {
		return err
	}
	existing, err := s.GetSecretStore(ctx, st.ID)
	if err != nil {
		return err
	}
	cipher := existing.Token
	if st.Token != "" && st.Token != SecretMask {
		cipher, err = s.sealStoreToken(st.Token)
		if err != nil {
			return err
		}
	}
	raw, err := marshalStoreConfig(*st)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE secret_stores SET
			name=$1, type=$2, config=$3, token_cipher=$4, updated_at=now()
		WHERE id=$5`,
		st.Name, st.Type, raw, cipher, st.ID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	st.Token = cipher
	return nil
}

func (s *Store) DeleteSecretStore(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM secret_stores WHERE id = $1`, id)
	return err
}

// SecretStoreByName returns the unique store with this name.
func (s *Store) SecretStoreByName(ctx context.Context, name string) (SecretStore, error) {
	stores, err := s.ListSecretStores(ctx)
	if err != nil {
		return SecretStore{}, err
	}
	var match []SecretStore
	for _, st := range stores {
		if st.Name == name {
			match = append(match, st)
		}
	}
	if len(match) == 0 {
		return SecretStore{}, pgx.ErrNoRows
	}
	if len(match) > 1 {
		return SecretStore{}, ambiguous("secret store", name)
	}
	return match[0], nil
}

// SecretStoreName resolves a store id to its name (empty if missing).
func (s *Store) SecretStoreName(ctx context.Context, id uuid.UUID) string {
	st, err := s.GetSecretStore(ctx, id)
	if err != nil {
		return ""
	}
	return st.Name
}
