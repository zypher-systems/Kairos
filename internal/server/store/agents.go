package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Agent is a polling worker registered on a remote host.
type Agent struct {
	ID              uuid.UUID         `json:"id"`
	Name            string            `json:"name"`
	TokenHash       string            `json:"-"`
	Hostname        string            `json:"hostname"`
	Labels          map[string]string `json:"labels"`
	Capabilities    map[string]string `json:"capabilities"`
	Version         string            `json:"version"`
	Status          string            `json:"status"`
	LastHeartbeatAt *time.Time        `json:"last_heartbeat_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

// NewAgentToken returns a fresh URL-safe registration token.
func NewAgentToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "kag_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken derives the stored token hash.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// UpsertAgentByToken registers (or re-registers) an agent, matching by token
// hash. Tokens are the agent identity; re-registration hands back the same id.
func (s *Store) UpsertAgentByToken(ctx context.Context, token, name, hostname, version string, labels, caps map[string]string) (Agent, error) {
	hash := HashToken(token)
	if labels == nil {
		labels = map[string]string{}
	}
	if caps == nil {
		caps = map[string]string{}
	}
	l, _ := json.Marshal(labels)
	c, _ := json.Marshal(caps)
	now := time.Now()
	var a Agent
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agents (name, token_hash, hostname, labels, capabilities, version, status, last_heartbeat_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'online', $7)
		ON CONFLICT (token_hash) DO UPDATE SET
			name = EXCLUDED.name,
			hostname = EXCLUDED.hostname,
			labels = EXCLUDED.labels,
			capabilities = EXCLUDED.capabilities,
			version = EXCLUDED.version,
			status = 'online',
			last_heartbeat_at = EXCLUDED.last_heartbeat_at
		RETURNING id, name, token_hash, hostname, labels, capabilities, version, status, last_heartbeat_at, created_at`,
		name, hash, hostname, l, c, version, now,
	).Scan(&a.ID, &a.Name, &a.TokenHash, &a.Hostname, &l, &c, &a.Version, &a.Status, &a.LastHeartbeatAt, &a.CreatedAt)
	if err != nil {
		return a, err
	}
	_ = json.Unmarshal(l, &a.Labels)
	_ = json.Unmarshal(c, &a.Capabilities)
	if a.Labels == nil {
		a.Labels = map[string]string{}
	}
	if a.Capabilities == nil {
		a.Capabilities = map[string]string{}
	}
	return a, nil
}

// CreateAgent issues a new agent identity and returns the row plus the
// one-time registration token (only shown at creation).
func (s *Store) CreateAgent(ctx context.Context, name string, labels map[string]string) (Agent, string, error) {
	token, err := NewAgentToken()
	if err != nil {
		return Agent{}, "", err
	}
	if labels == nil {
		labels = map[string]string{}
	}
	l, err := json.Marshal(labels)
	if err != nil {
		return Agent{}, "", err
	}
	hash := HashToken(token)
	var a Agent
	now := time.Now()
	err = s.pool.QueryRow(ctx, `
		INSERT INTO agents (name, token_hash, labels, status, last_heartbeat_at)
		VALUES ($1, $2, $3, 'offline', $4)
		RETURNING id, name, token_hash, hostname, labels, capabilities, version, status, last_heartbeat_at, created_at`,
		name, hash, l, now,
	).Scan(&a.ID, &a.Name, &a.TokenHash, &a.Hostname, &l, &a.Capabilities, &a.Version, &a.Status, &a.LastHeartbeatAt, &a.CreatedAt)
	if err != nil {
		return Agent{}, "", err
	}
	var caps []byte
	_ = caps
	_ = json.Unmarshal(l, &a.Labels)
	if a.Labels == nil {
		a.Labels = map[string]string{}
	}
	if a.Capabilities == nil {
		a.Capabilities = map[string]string{}
	}
	return a, token, nil
}

// GetAgentByTokenHash resolves an agent by token hash (auth path).
func (s *Store) GetAgentByTokenHash(ctx context.Context, hash string) (Agent, error) {
	var a Agent
	var l, c []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, token_hash, hostname, labels, capabilities, version, status, last_heartbeat_at, created_at
		FROM agents WHERE token_hash = $1`, hash,
	).Scan(&a.ID, &a.Name, &a.TokenHash, &a.Hostname, &l, &c, &a.Version, &a.Status, &a.LastHeartbeatAt, &a.CreatedAt)
	if err != nil {
		return a, err
	}
	_ = json.Unmarshal(l, &a.Labels)
	_ = json.Unmarshal(c, &a.Capabilities)
	return a, nil
}

// GetAgentByID fetches one agent.
func (s *Store) GetAgentByID(ctx context.Context, id uuid.UUID) (Agent, error) {
	var a Agent
	var l, c []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, token_hash, hostname, labels, capabilities, version, status, last_heartbeat_at, created_at
		FROM agents WHERE id = $1`, id,
	).Scan(&a.ID, &a.Name, &a.TokenHash, &a.Hostname, &l, &c, &a.Version, &a.Status, &a.LastHeartbeatAt, &a.CreatedAt)
	if err != nil {
		return a, err
	}
	_ = json.Unmarshal(l, &a.Labels)
	_ = json.Unmarshal(c, &a.Capabilities)
	return a, nil
}

// ListAgents returns all agents, newest first.
func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, token_hash, hostname, labels, capabilities, version, status, last_heartbeat_at, created_at
		FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		var l, c []byte
		if err := rows.Scan(&a.ID, &a.Name, &a.TokenHash, &a.Hostname, &l, &c, &a.Version, &a.Status, &a.LastHeartbeatAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(l, &a.Labels)
		_ = json.Unmarshal(c, &a.Capabilities)
		if a.Labels == nil {
			a.Labels = map[string]string{}
		}
		if a.Capabilities == nil {
			a.Capabilities = map[string]string{}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TouchAgentHeartbeat updates liveness.
func (s *Store) TouchAgentHeartbeat(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agents SET status = 'online', last_heartbeat_at = now() WHERE id = $1`, id)
	return err
}

// MarkStaleAgents flags agents offline whose heartbeat is older than d.
func (s *Store) MarkStaleAgents(ctx context.Context, d time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agents SET status = 'offline'
		WHERE status = 'online' AND (last_heartbeat_at IS NULL OR last_heartbeat_at < now() - $1 * interval '1 millisecond')`,
		d.Milliseconds(),
	)
	return err
}

// DeleteAgent removes an agent (work targeted at it becomes unclaimable).
func (s *Store) DeleteAgent(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
	return err
}

// CountOnlineAgents is the number of agents currently marked online.
func (s *Store) CountOnlineAgents(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM agents WHERE status = 'online'`).Scan(&n)
	return n, err
}

// UpdateAgentLabels replaces labels (optional convenience for label targeting).
func (s *Store) UpdateAgentLabels(ctx context.Context, id uuid.UUID, labels map[string]string) error {
	l, err := json.Marshal(labels)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE agents SET labels = $2 WHERE id = $1`, id, l)
	return err
}

// UpdateAgentAttrs refreshes the attributes an agent reports about itself.
// Labels are not among them: they route work and are owned by the server.
func (s *Store) UpdateAgentAttrs(ctx context.Context, id uuid.UUID, name, hostname, version string, caps map[string]string) error {
	if caps == nil {
		caps = map[string]string{}
	}
	c, err := json.Marshal(caps)
	if err != nil {
		return err
	}
	q := `UPDATE agents SET status = 'online', last_heartbeat_at = now()`
	var args []any = []any{id}
	if name != "" {
		q += `, name = $` + itoa(len(args)+1)
		args = append(args, name)
	}
	if hostname != "" {
		q += `, hostname = $` + itoa(len(args)+1)
		args = append(args, hostname)
	}
	if version != "" {
		q += `, version = $` + itoa(len(args)+1)
		args = append(args, version)
	}
	q += `, capabilities = $` + itoa(len(args)+1)
	args = append(args, c)
	q += ` WHERE id = $1`
	_, err = s.pool.Exec(ctx, q, args...)
	return err
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

var _ = pgx.ErrNoRows
