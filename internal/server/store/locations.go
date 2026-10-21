package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StorageLocation is a place files live, referenced by transfers and file
// triggers. Kinds:
//
//   - mount: a directory on the Kairos server, usually an NFS or SMB share
//     mounted into the container. The server executes.
//   - sftp:  a directory on an SSH host, using that host's credentials.
//     The server executes, or an agent when the other end is an agent.
//   - s3:    a prefix in an S3-compatible bucket. Server or agent executes.
//   - agent: a directory on an agent machine (its disk, or a share mounted
//     there). The agent executes.
type StorageLocation struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// mount, sftp, agent: base path. s3: key prefix.
	BasePath string `json:"base_path"`
	// sftp
	SSHHostID *uuid.UUID `json:"ssh_host_id,omitempty"`
	// agent
	AgentID *uuid.UUID `json:"agent_id,omitempty"`
	// nfs: Host is the server, Export the exported directory; UID/GID the
	// identity the export sees.
	Host   string `json:"host,omitempty"`
	Export string `json:"export,omitempty"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
	// smb: Host, Share, Domain, Username; the password is SecretKey.
	Share    string `json:"share,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Username string `json:"username,omitempty"`
	// s3
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	PathStyle bool   `json:"path_style,omitempty"`
	Insecure  bool   `json:"insecure,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	// SecretKey (the S3 secret or the SMB password) is write-only: masked
	// in responses, sealed at rest.
	SecretKey    string `json:"secret_key,omitempty"`
	HasSecretKey bool   `json:"has_secret_key"`
}

type locationConfig struct {
	BasePath  string `json:"base_path"`
	SSHHostID string `json:"ssh_host_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Host      string `json:"host,omitempty"`
	Export    string `json:"export,omitempty"`
	UID       uint32 `json:"uid,omitempty"`
	GID       uint32 `json:"gid,omitempty"`
	Share     string `json:"share,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Username  string `json:"username,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	PathStyle bool   `json:"path_style,omitempty"`
	Insecure  bool   `json:"insecure,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
}

// Validate checks the fields a kind needs.
func (l *StorageLocation) Validate() error {
	l.Name = strings.TrimSpace(l.Name)
	l.BasePath = strings.TrimSpace(l.BasePath)
	if l.Name == "" {
		return errors.New("name is required")
	}
	if len(l.Name) > 120 {
		return errors.New("name is too long")
	}
	switch l.Kind {
	case "mount":
		if !strings.HasPrefix(l.BasePath, "/") {
			return errors.New("base_path must be an absolute directory on the Kairos server")
		}
	case "sftp":
		if l.SSHHostID == nil {
			return errors.New("ssh_host_id is required for sftp locations")
		}
		if l.BasePath != "" && !strings.HasPrefix(l.BasePath, "/") {
			return errors.New("base_path must be absolute on the host, or empty")
		}
	case "agent":
		if l.AgentID == nil {
			return errors.New("agent_id is required for agent locations")
		}
		if !strings.HasPrefix(l.BasePath, "/") {
			return errors.New("base_path must be an absolute directory on the agent machine")
		}
	case "nfs":
		l.Host = strings.TrimSpace(l.Host)
		l.Export = strings.TrimSpace(l.Export)
		if l.Host == "" {
			return errors.New("host is required for nfs locations")
		}
		if !strings.HasPrefix(l.Export, "/") {
			return errors.New("export must be the exported path on the server, e.g. /srv/nfs/incoming")
		}
		l.BasePath = strings.Trim(l.BasePath, "/")
	case "smb":
		l.Host = strings.TrimSpace(l.Host)
		l.Share = strings.Trim(strings.TrimSpace(l.Share), "/\\")
		l.Domain = strings.TrimSpace(l.Domain)
		l.Username = strings.TrimSpace(l.Username)
		if l.Host == "" {
			return errors.New("host is required for smb locations")
		}
		if l.Share == "" || strings.ContainsAny(l.Share, "/\\") {
			return errors.New("share must be the share name only, e.g. exports")
		}
		l.BasePath = strings.Trim(strings.ReplaceAll(l.BasePath, "\\", "/"), "/")
	case "s3":
		l.Bucket = strings.TrimSpace(l.Bucket)
		l.Endpoint = strings.TrimSpace(l.Endpoint)
		l.Region = strings.TrimSpace(l.Region)
		if l.Bucket == "" {
			return errors.New("bucket is required for s3 locations")
		}
		if l.Endpoint == "" && l.Region == "" {
			return errors.New("region is required for AWS S3 (or set an endpoint)")
		}
		l.BasePath = strings.Trim(l.BasePath, "/")
	default:
		return errors.New("kind must be mount, nfs, smb, sftp, s3, or agent")
	}
	return nil
}

func (l StorageLocation) config() ([]byte, error) {
	c := locationConfig{
		BasePath: l.BasePath, Endpoint: l.Endpoint, Region: l.Region, Bucket: l.Bucket,
		PathStyle: l.PathStyle, Insecure: l.Insecure, AccessKey: l.AccessKey,
		Host: l.Host, Export: l.Export, UID: l.UID, GID: l.GID,
		Share: l.Share, Domain: l.Domain, Username: l.Username,
	}
	if l.SSHHostID != nil {
		c.SSHHostID = l.SSHHostID.String()
	}
	if l.AgentID != nil {
		c.AgentID = l.AgentID.String()
	}
	return json.Marshal(c)
}

const locationCols = `id, name, kind, config, secret_cipher, created_at, updated_at`

func scanLocation(row pgx.Row) (StorageLocation, error) {
	var l StorageLocation
	var raw []byte
	var cipher string
	if err := row.Scan(&l.ID, &l.Name, &l.Kind, &raw, &cipher, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return l, err
	}
	var c locationConfig
	_ = json.Unmarshal(raw, &c)
	l.BasePath, l.Endpoint, l.Region, l.Bucket = c.BasePath, c.Endpoint, c.Region, c.Bucket
	l.PathStyle, l.Insecure, l.AccessKey = c.PathStyle, c.Insecure, c.AccessKey
	l.Host, l.Export, l.UID, l.GID = c.Host, c.Export, c.UID, c.GID
	l.Share, l.Domain, l.Username = c.Share, c.Domain, c.Username
	if id, err := uuid.Parse(c.SSHHostID); err == nil {
		l.SSHHostID = &id
	}
	if id, err := uuid.Parse(c.AgentID); err == nil {
		l.AgentID = &id
	}
	l.SecretKey = cipher
	l.HasSecretKey = cipher != ""
	return l, nil
}

// CreateStorageLocation stores a location; the secret key is sealed.
func (s *Store) CreateStorageLocation(ctx context.Context, l *StorageLocation) error {
	if err := l.Validate(); err != nil {
		return err
	}
	cipher, err := s.sealStoreToken(l.SecretKey)
	if err != nil {
		return err
	}
	raw, err := l.config()
	if err != nil {
		return err
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO storage_locations (name, kind, config, secret_cipher)
		VALUES ($1,$2,$3,$4)
		RETURNING id, created_at, updated_at`,
		l.Name, l.Kind, raw, cipher,
	).Scan(&l.ID, &l.CreatedAt, &l.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "storage_locations_name_key") {
		return fmt.Errorf("a location named %q already exists", l.Name)
	}
	l.SecretKey = ""
	l.HasSecretKey = cipher != ""
	return err
}

// UpdateStorageLocation replaces a location. An empty or masked secret
// key keeps the stored one.
func (s *Store) UpdateStorageLocation(ctx context.Context, l *StorageLocation) error {
	if err := l.Validate(); err != nil {
		return err
	}
	raw, err := l.config()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var cipher string
	if err := tx.QueryRow(ctx, `SELECT secret_cipher FROM storage_locations WHERE id = $1 FOR UPDATE`, l.ID).Scan(&cipher); err != nil {
		return err
	}
	if strings.TrimSpace(l.SecretKey) != "" && l.SecretKey != SecretMask {
		cipher, err = s.sealStoreToken(l.SecretKey)
		if err != nil {
			return err
		}
	}
	if l.Kind != "s3" && l.Kind != "smb" {
		cipher = ""
	}
	err = tx.QueryRow(ctx, `
		UPDATE storage_locations SET name = $2, kind = $3, config = $4, secret_cipher = $5, updated_at = now()
		WHERE id = $1 RETURNING created_at, updated_at`,
		l.ID, l.Name, l.Kind, raw, cipher,
	).Scan(&l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "storage_locations_name_key") {
			return fmt.Errorf("a location named %q already exists", l.Name)
		}
		return err
	}
	l.SecretKey = ""
	l.HasSecretKey = cipher != ""
	return tx.Commit(ctx)
}

// GetStorageLocation returns a location with the secret masked.
func (s *Store) GetStorageLocation(ctx context.Context, id uuid.UUID) (StorageLocation, error) {
	l, err := scanLocation(s.pool.QueryRow(ctx, `SELECT `+locationCols+` FROM storage_locations WHERE id = $1`, id))
	if err != nil {
		return l, err
	}
	MaskStorageLocation(&l)
	return l, nil
}

// GetStorageLocationForDelivery returns a location with the secret revealed.
func (s *Store) GetStorageLocationForDelivery(ctx context.Context, id uuid.UUID) (StorageLocation, error) {
	l, err := scanLocation(s.pool.QueryRow(ctx, `SELECT `+locationCols+` FROM storage_locations WHERE id = $1`, id))
	if err != nil {
		return l, err
	}
	if l.SecretKey != "" {
		key := s.revealStoreToken(l.SecretKey)
		if key == "" {
			return l, fmt.Errorf("%w: storage location %s", ErrSecretUnreadable, l.Name)
		}
		l.SecretKey = key
	}
	return l, nil
}

// ListStorageLocations lists locations by name with secrets masked.
func (s *Store) ListStorageLocations(ctx context.Context) ([]StorageLocation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+locationCols+` FROM storage_locations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StorageLocation
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		MaskStorageLocation(&l)
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteStorageLocation removes a location. Jobs that reference it keep
// their configuration and fail at run time with a clear message.
func (s *Store) DeleteStorageLocation(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM storage_locations WHERE id = $1`, id)
	return err
}

// StorageLocationByName resolves a name (jobs-as-code).
func (s *Store) StorageLocationByName(ctx context.Context, name string) (StorageLocation, error) {
	l, err := scanLocation(s.pool.QueryRow(ctx, `SELECT `+locationCols+` FROM storage_locations WHERE name = $1`, strings.TrimSpace(name)))
	if err != nil {
		return l, err
	}
	MaskStorageLocation(&l)
	return l, nil
}

// StorageLocationName returns the name for an id, or "" when unknown.
func (s *Store) StorageLocationName(ctx context.Context, id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	var name string
	_ = s.pool.QueryRow(ctx, `SELECT name FROM storage_locations WHERE id = $1`, *id).Scan(&name)
	return name
}

// MaskStorageLocation hides the secret key for API responses.
func MaskStorageLocation(l *StorageLocation) {
	if l.SecretKey != "" {
		l.SecretKey = SecretMask
	}
}
