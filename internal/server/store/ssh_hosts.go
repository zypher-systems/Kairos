package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SSHHost is a remote host the server pushes work to over SSH.
// Private material is encrypted at rest via the store crypto box.
type SSHHost struct {
	ID                 uuid.UUID  `json:"id"`
	Name               string     `json:"name"`
	Host               string     `json:"host"`
	Port               int        `json:"port"`
	Username           string     `json:"username"`
	AuthType           string     `json:"auth_type"` // key | password | managed_key
	SSHKeyID           *uuid.UUID `json:"ssh_key_id,omitempty"`
	SSHKeyName         string     `json:"ssh_key_name,omitempty"`
	HasPrivateKey      bool       `json:"has_private_key"`
	HasPassword        bool       `json:"has_password"`
	HostKeyFingerprint string     `json:"host_key_fingerprint"`
	VerifyHostKey      bool       `json:"verify_host_key"`
	// ForgetHostKey is transient: on update it clears the pinned
	// fingerprint so the next connection pins a new one.
	ForgetHostKey bool       `json:"-"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	// PrivateKey / Password are transient: only accepted on create/update,
	// decrypted on demand, never serialized out.
}

// Validate checks host invariants.
func (h *SSHHost) Validate() error {
	if h.Name == "" {
		return errors.New("name is required")
	}
	if h.Host == "" {
		return errors.New("host is required")
	}
	if h.Port == 0 {
		h.Port = 22
	}
	if h.Username == "" {
		h.Username = "root"
	}
	if h.AuthType == "" {
		h.AuthType = "key"
	}
	switch h.AuthType {
	case "key", "password":
		h.SSHKeyID = nil
	case "managed_key":
		if h.SSHKeyID == nil {
			return errors.New("ssh_key_id required for managed_key auth")
		}
	default:
		return errors.New("auth_type must be key, password, or managed_key")
	}
	return nil
}

// CreateSSHHostTx stores a host; the raw key or password is encrypted here.
// Caller passes the cleartext once; the store keeps only ciphertext.
func (s *Store) CreateSSHHost(ctx context.Context, h *SSHHost, privateKey, password string) error {
	if err := h.Validate(); err != nil {
		return err
	}
	var keyEnc, pwEnc *string
	if privateKey != "" {
		if s.box == nil {
			return errors.New("crypto box not configured")
		}
		sealed, err := s.box.Seal([]byte(privateKey))
		if err != nil {
			return err
		}
		keyEnc = &sealed
	}
	if password != "" {
		if s.box == nil {
			return errors.New("crypto box not configured")
		}
		sealed, err := s.box.Seal([]byte(password))
		if err != nil {
			return err
		}
		pwEnc = &sealed
	}
	var rowIDErr = s.pool.QueryRow(ctx, `
		INSERT INTO ssh_hosts (name, host, port, username, auth_type, ssh_key_id, private_key_encrypted,
			password_encrypted, host_key_fingerprint, verify_host_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		h.Name, h.Host, h.Port, h.Username, h.AuthType, h.SSHKeyID,
		nullableString(keyEnc), nullableString(pwEnc), h.HostKeyFingerprint, h.VerifyHostKey,
	).Scan(&h.ID)
	return rowIDErr
}

// UpdateSSHHost applies changes; blank key/password leave stored values intact.
// A blank fingerprint keeps the pinned one unless ForgetHostKey is set or the
// host/port changed, so an edit never silently drops the TOFU pin.
func (s *Store) UpdateSSHHost(ctx context.Context, h *SSHHost, privateKey, password string) error {
	if err := h.Validate(); err != nil {
		return err
	}
	var keyEnc, pwEnc *string
	if privateKey != "" {
		if s.box == nil {
			return errors.New("crypto box not configured")
		}
		sealed, err := s.box.Seal([]byte(privateKey))
		if err != nil {
			return err
		}
		keyEnc = &sealed
	}
	if password != "" {
		if s.box == nil {
			return errors.New("crypto box not configured")
		}
		sealed, err := s.box.Seal([]byte(password))
		if err != nil {
			return err
		}
		pwEnc = &sealed
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE ssh_hosts SET
			name=$1, host=$2, port=$3, username=$4, auth_type=$5, ssh_key_id=$6,
			private_key_encrypted = COALESCE($7, private_key_encrypted),
			password_encrypted = COALESCE($8, password_encrypted),
			host_key_fingerprint = CASE
				WHEN $12 THEN ''
				WHEN $9 <> '' THEN $9
				WHEN host <> $2 OR port <> $3 THEN ''
				ELSE host_key_fingerprint END,
			verify_host_key=$10, updated_at=now()
		WHERE id=$11`,
		h.Name, h.Host, h.Port, h.Username, h.AuthType, h.SSHKeyID,
		nullableString(keyEnc), nullableString(pwEnc), h.HostKeyFingerprint, h.VerifyHostKey, h.ID, h.ForgetHostKey,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// sshHostSelect is the shared column list for host metadata reads.
const sshHostSelect = `
	SELECT h.id, h.name, h.host, h.port, h.username, h.auth_type, h.ssh_key_id,
		COALESCE(k.name, ''),
		h.private_key_encrypted IS NOT NULL, h.password_encrypted IS NOT NULL,
		h.host_key_fingerprint, h.verify_host_key, h.last_seen_at, h.created_at
	FROM ssh_hosts h
	LEFT JOIN ssh_keys k ON k.id = h.ssh_key_id`

func scanSSHHost(row pgx.Row) (SSHHost, error) {
	var h SSHHost
	err := row.Scan(
		&h.ID, &h.Name, &h.Host, &h.Port, &h.Username, &h.AuthType, &h.SSHKeyID,
		&h.SSHKeyName,
		&h.HasPrivateKey, &h.HasPassword,
		&h.HostKeyFingerprint, &h.VerifyHostKey, &h.LastSeenAt, &h.CreatedAt,
	)
	return h, err
}

// GetSSHHost fetches metadata; secrets stay out of the struct.
func (s *Store) GetSSHHost(ctx context.Context, id uuid.UUID) (SSHHost, error) {
	return scanSSHHost(s.pool.QueryRow(ctx, sshHostSelect+` WHERE h.id = $1`, id))
}

// GetSSHCredentials decrypts stored credentials for an outbound connection.
// For managed_key hosts the private half comes from the referenced key.
func (s *Store) GetSSHCredentials(ctx context.Context, id uuid.UUID) (privateKey, password, fingerprint string, ok bool, err error) {
	if s.box == nil {
		return "", "", "", false, errors.New("crypto box not configured")
	}
	var keyEnc, pwEnc []byte
	var authType string
	var keyID *uuid.UUID
	err = s.pool.QueryRow(ctx, `
		SELECT auth_type, ssh_key_id, private_key_encrypted, password_encrypted, host_key_fingerprint
		FROM ssh_hosts WHERE id = $1`, id,
	).Scan(&authType, &keyID, &keyEnc, &pwEnc, &fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}

	if authType == "managed_key" && keyID != nil {
		pem, err := s.GetSSHKeyPrivate(ctx, *keyID)
		if err != nil {
			return "", "", "", false, err
		}
		privateKey = pem
	} else {
		if keyEnc != nil {
			raw, err := s.box.Open(string(keyEnc))
			if err != nil {
				return "", "", "", false, err
			}
			privateKey = string(raw)
		}
		if pwEnc != nil {
			raw, err := s.box.Open(string(pwEnc))
			if err != nil {
				return "", "", "", false, err
			}
			password = string(raw)
		}
	}
	return privateKey, password, fingerprint, true, nil
}

// ListSSHHosts returns all host metadata.
func (s *Store) ListSSHHosts(ctx context.Context) ([]SSHHost, error) {
	rows, err := s.pool.Query(ctx, sshHostSelect+` ORDER BY h.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SSHHost
	for rows.Next() {
		h, err := scanSSHHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DeleteSSHHost removes a host.
func (s *Store) DeleteSSHHost(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM ssh_hosts WHERE id = $1`, id)
	return err
}

// SetSSHHostFingerprint pins a host key on first use (TOFU). It only writes
// when no key is pinned yet, so two first connections racing each other
// cannot replace a pin that one of them already stored.
func (s *Store) SetSSHHostFingerprint(ctx context.Context, id uuid.UUID, fp string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE ssh_hosts SET host_key_fingerprint = $2, verify_host_key = true, updated_at = now()
		WHERE id = $1 AND host_key_fingerprint = ''`, id, fp)
	return err
}

func nullableString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
