package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// NotificationChannel is a delivery endpoint for run events. Sensitive
// config values (secrets, passwords, webhook URLs) are encrypted at rest
// and masked in API reads.
type NotificationChannel struct {
	ID               uuid.UUID         `json:"id"`
	Name             string            `json:"name"`
	Type             string            `json:"type"` // webhook | discord | slack | smtp
	Config           map[string]string `json:"config"`
	Enabled          bool              `json:"enabled"`
	NotifyOnFailure  bool              `json:"notify_on_failure"`
	NotifyOnSuccess  bool              `json:"notify_on_success"`
	NotifyOnMissed   bool              `json:"notify_on_missed"`
	NotifyOnLate     bool              `json:"notify_on_late"`
	NotifyOnPending  bool              `json:"notify_on_pending"`
	NotifyOnRejected bool              `json:"notify_on_rejected"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// Validate checks channel invariants.
func (c *NotificationChannel) Validate() error {
	if c.Name == "" {
		return errors.New("name is required")
	}
	switch c.Type {
	case "webhook":
		if c.Config["url"] == "" {
			return errors.New("config.url is required for webhook channels")
		}
	case "discord", "slack":
		if c.Config["webhook_url"] == "" {
			return errors.New("config.webhook_url is required for " + c.Type + " channels")
		}
	case "smtp":
		for _, k := range []string{"host", "from", "to"} {
			if c.Config[k] == "" {
				return errors.New("config." + k + " is required for smtp channels")
			}
		}
	default:
		return errors.New("type must be webhook, discord, slack, or smtp")
	}
	return nil
}

// secretConfigKeys are config fields encrypted at rest and masked on read.
// Webhook URLs for Discord/Slack are bearer secrets (they contain tokens).
var secretConfigKeys = map[string]bool{
	"webhook_url": true,
	"secret":      true,
	"password":    true,
}

const encPrefix = "enc:v1:"

// sealConfig encrypts sensitive config values in place.
func (s *Store) sealConfig(cfg map[string]string) error {
	if s.box == nil {
		return nil
	}
	for k, v := range cfg {
		if !secretConfigKeys[k] || v == "" || v == SecretMask || strings.HasPrefix(v, encPrefix) {
			continue
		}
		sealed, err := s.box.Seal([]byte(v))
		if err != nil {
			return err
		}
		cfg[k] = encPrefix + sealed
	}
	return nil
}

// revealConfig decrypts sensitive values for delivery.
func (s *Store) revealConfig(cfg map[string]string) {
	if s.box == nil {
		return
	}
	for k, v := range cfg {
		if !strings.HasPrefix(v, encPrefix) {
			continue
		}
		raw, err := s.box.Open(strings.TrimPrefix(v, encPrefix))
		if err != nil {
			cfg[k] = ""
			continue
		}
		cfg[k] = string(raw)
	}
}

// MaskChannelSecrets redacts sensitive config values for API responses.
func MaskChannelSecrets(c *NotificationChannel) {
	for k, v := range c.Config {
		if secretConfigKeys[k] && v != "" {
			c.Config[k] = SecretMask
		}
	}
}

func (s *Store) CreateChannel(ctx context.Context, c *NotificationChannel) error {
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	if err := c.Validate(); err != nil {
		return err
	}
	cfg := mapsClone(c.Config)
	if err := s.sealConfig(cfg); err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `
		INSERT INTO notifications (name, type, config, enabled, notify_on_failure, notify_on_success, notify_on_missed, notify_on_late, notify_on_pending, notify_on_rejected)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at, updated_at`,
		c.Name, c.Type, raw, c.Enabled, c.NotifyOnFailure, c.NotifyOnSuccess, c.NotifyOnMissed, c.NotifyOnLate, c.NotifyOnPending, c.NotifyOnRejected,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

const channelCols = `id, name, type, config, enabled, notify_on_failure, notify_on_success, notify_on_missed, notify_on_late, notify_on_pending, notify_on_rejected, created_at, updated_at`

func scanChannel(row pgx.Row) (NotificationChannel, error) {
	var c NotificationChannel
	var raw []byte
	err := row.Scan(&c.ID, &c.Name, &c.Type, &raw, &c.Enabled,
		&c.NotifyOnFailure, &c.NotifyOnSuccess, &c.NotifyOnMissed, &c.NotifyOnLate,
		&c.NotifyOnPending, &c.NotifyOnRejected, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	c.Config = map[string]string{}
	_ = json.Unmarshal(raw, &c.Config)
	return c, nil
}

func (s *Store) GetChannel(ctx context.Context, id uuid.UUID) (NotificationChannel, error) {
	return scanChannel(s.pool.QueryRow(ctx, `SELECT `+channelCols+` FROM notifications WHERE id = $1`, id))
}

// GetChannelForDelivery loads a channel with secrets revealed.
func (s *Store) GetChannelForDelivery(ctx context.Context, id uuid.UUID) (NotificationChannel, error) {
	c, err := s.GetChannel(ctx, id)
	if err != nil {
		return c, err
	}
	s.revealConfig(c.Config)
	return c, nil
}

func (s *Store) ListChannels(ctx context.Context) ([]NotificationChannel, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+channelCols+` FROM notifications ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChannelsForEvent returns enabled channels subscribed to an event,
// secrets revealed for delivery.
func (s *Store) ChannelsForEvent(ctx context.Context, event string) ([]NotificationChannel, error) {
	col := "notify_on_failure"
	switch event {
	case "run_succeeded":
		col = "notify_on_success"
	case "run_missed":
		col = "notify_on_missed"
	case "run_late":
		col = "notify_on_late"
	case "run_pending":
		col = "notify_on_pending"
	case "run_rejected":
		col = "notify_on_rejected"
	}
	rows, err := s.pool.Query(ctx, `SELECT `+channelCols+` FROM notifications WHERE enabled AND `+col)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		s.revealConfig(c.Config)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateChannel(ctx context.Context, c *NotificationChannel) error {
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	if err := c.Validate(); err != nil {
		return err
	}
	// Preserve stored secrets when the client sends masked/empty values.
	existing, err := s.GetChannel(ctx, c.ID)
	if err != nil {
		return err
	}
	for k, v := range c.Config {
		if secretConfigKeys[k] && (v == "" || v == SecretMask) {
			c.Config[k] = existing.Config[k]
		}
	}
	if err := s.sealConfig(c.Config); err != nil {
		return err
	}
	raw, err := json.Marshal(c.Config)
	if err != nil {
		return err
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE notifications SET
			name=$1, type=$2, config=$3, enabled=$4,
			notify_on_failure=$5, notify_on_success=$6, notify_on_missed=$7, notify_on_late=$8,
			notify_on_pending=$9, notify_on_rejected=$10, updated_at=now()
		WHERE id=$11`,
		c.Name, c.Type, raw, c.Enabled, c.NotifyOnFailure, c.NotifyOnSuccess, c.NotifyOnMissed, c.NotifyOnLate,
		c.NotifyOnPending, c.NotifyOnRejected, c.ID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteChannel(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM notifications WHERE id = $1`, id)
	return err
}

func mapsClone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
