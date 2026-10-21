package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AuditEvent is one immutable trail row.
type AuditEvent struct {
	ID            uuid.UUID  `json:"id"`
	ActorID       *uuid.UUID `json:"actor_id,omitempty"`
	ActorUsername string     `json:"actor_username"`
	Action        string     `json:"action"`
	ObjectType    string     `json:"object_type"`
	ObjectID      string     `json:"object_id"`
	Detail        string     `json:"detail"`
	IP            string     `json:"ip"`
	CreatedAt     time.Time  `json:"created_at"`
}

// AppendAudit writes one event. Failures are returned; callers usually ignore them.
func (s *Store) AppendAudit(ctx context.Context, ev AuditEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (actor_id, actor_username, action, object_type, object_id, detail, ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ev.ActorID, ev.ActorUsername, ev.Action, ev.ObjectType, ev.ObjectID, ev.Detail, ev.IP,
	)
	return err
}

// ListAudit returns newest-first events.
func (s *Store) ListAudit(ctx context.Context, limit, offset int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_id, actor_username, action, object_type, object_id, detail, ip, created_at
		FROM audit_events
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var ev AuditEvent
		if err := rows.Scan(&ev.ID, &ev.ActorID, &ev.ActorUsername, &ev.Action, &ev.ObjectType, &ev.ObjectID, &ev.Detail, &ev.IP, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
