package store

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"

	"github.com/zypher-systems/kairos/internal/server/sshx"
)

// SSHKey is a managed keypair. The private half never leaves the store
// layer; API responses carry metadata only.
type SSHKey struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	PublicKey   string    `json:"public_key"` // authorized_keys line
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

// sshKeyCols are the API-safe columns (never the encrypted private half).
const sshKeyCols = `id, name, public_key, fingerprint, created_at`

func scanSSHKey(row pgx.Row) (SSHKey, error) {
	var k SSHKey
	err := row.Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt)
	return k, err
}

// GenerateSSHKeypair creates a fresh ed25519 keypair. Returns the private
// key PEM (caller seals it) and the authorized_keys public line.
func GenerateSSHKeypair(comment string) (privatePEM, publicLine, fingerprint string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", "", err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	line := string(ssh.MarshalAuthorizedKey(sshPub))
	// MarshalAuthorizedKey appends a newline; authorized_keys comment goes at EOL.
	line = line[:len(line)-1] + " " + comment
	return string(pemBytes), line, sshx.FingerprintSHA256(sshPub), nil
}

// ParseSSHPublicKey validates an OpenSSH authorized_keys line and returns
// its fingerprint.
func ParseSSHPublicKey(line string) (string, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return "", fmt.Errorf("invalid public key: %w", err)
	}
	return sshx.FingerprintSHA256(pub), nil
}

// CreateSSHKey stores a keypair; the private key is encrypted here.
func (s *Store) CreateSSHKey(ctx context.Context, k *SSHKey, privateKeyPEM string) error {
	if k.Name == "" {
		return errors.New("name is required")
	}
	if privateKeyPEM == "" {
		return errors.New("private key is required")
	}
	if s.box == nil {
		return errors.New("crypto box not configured")
	}
	// Validate the private key parses; the public half is derived from it
	// (authoritative — a pasted public key would only be a comment).
	signer, err := ssh.ParsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return fmt.Errorf("invalid private key: %w", err)
	}
	pub := signer.PublicKey()
	k.PublicKey = string(ssh.MarshalAuthorizedKey(pub))
	k.Fingerprint = sshx.FingerprintSHA256(pub)

	sealed, err := s.box.Seal([]byte(privateKeyPEM))
	if err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `
		INSERT INTO ssh_keys (name, public_key, private_key_enc, fingerprint)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`,
		k.Name, k.PublicKey, sealed, k.Fingerprint,
	).Scan(&k.ID, &k.CreatedAt)
}

// ListSSHKeys returns metadata for all managed keys.
func (s *Store) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sshKeyCols+` FROM ssh_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SSHKey
	for rows.Next() {
		k, err := scanSSHKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetSSHKey fetches one key's metadata.
func (s *Store) GetSSHKey(ctx context.Context, id uuid.UUID) (SSHKey, error) {
	return scanSSHKey(s.pool.QueryRow(ctx, `SELECT `+sshKeyCols+` FROM ssh_keys WHERE id = $1`, id))
}

// DeleteSSHKey removes a key. Hosts referencing it keep working via their
// inline credentials but lose the managed reference.
func (s *Store) DeleteSSHKey(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM ssh_keys WHERE id = $1`, id)
	return err
}

// GetSSHKeyPrivate decrypts a managed key's private half for dialing.
func (s *Store) GetSSHKeyPrivate(ctx context.Context, id uuid.UUID) (string, error) {
	if s.box == nil {
		return "", errors.New("crypto box not configured")
	}
	var enc []byte
	err := s.pool.QueryRow(ctx, `SELECT private_key_enc FROM ssh_keys WHERE id = $1`, id).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("ssh key not found")
	}
	if err != nil {
		return "", err
	}
	raw, err := s.box.Open(string(enc))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// CountHostsUsingKey reports how many hosts reference a managed key (used
// to warn before deletion).
func (s *Store) CountHostsUsingKey(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM ssh_hosts WHERE ssh_key_id = $1`, id).Scan(&n)
	return n, err
}
