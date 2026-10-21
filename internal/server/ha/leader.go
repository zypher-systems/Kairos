// Package ha elects a single scheduler leader across Kairos server replicas.
package ha

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	leaseName = "scheduler"
	// DefaultTTL is how long a holder remains leader without a renew.
	DefaultTTL = 15 * time.Second
	// DefaultRenew is how often a replica tries to take or keep the lease.
	DefaultRenew = 5 * time.Second
)

// Leader holds (or follows) the scheduler lease row.
type Leader struct {
	pool  *pgxpool.Pool
	id    string
	ttl   time.Duration
	renew time.Duration
	mu    sync.RWMutex
	held  bool
	until time.Time
	// deadline is when this replica must stop acting as leader, on its own
	// clock: the lease TTL counted from just before the last successful
	// renew, minus a margin. If renewals fail (a database stall), IsLeader
	// turns false by itself before another replica can take the lease.
	deadline time.Time
	change   func(bool)
}

// New builds a leader elector. instanceID is generated when empty.
func New(pool *pgxpool.Pool, instanceID string, ttl, renew time.Duration) *Leader {
	if instanceID == "" {
		instanceID = uuid.NewString()
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if renew <= 0 {
		renew = DefaultRenew
	}
	return &Leader{pool: pool, id: instanceID, ttl: ttl, renew: renew}
}

// InstanceID is this process's lease holder id.
func (l *Leader) InstanceID() string { return l.id }

// IsLeader reports whether this replica currently holds the scheduler lease.
func (l *Leader) IsLeader() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.held && time.Now().Before(l.deadline)
}

// LeaseUntil is when the current lease expires (zero if not held).
func (l *Leader) LeaseUntil() time.Time {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.until
}

// SetOnChange registers a callback invoked when leadership is gained or lost.
func (l *Leader) SetOnChange(fn func(bool)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.change = fn
}

// BootPolicy records whether this replica is the scheduler leader.
// Followers keep serving HTTP; only the leader runs the scheduler.
func (l *Leader) BootPolicy() error {
	if !l.IsLeader() {
		slog.Info("ha: follower, serving HTTP only", "instance", l.id)
	}
	return nil
}

// TryAcquire attempts to take or renew the scheduler lease.
func (l *Leader) TryAcquire(ctx context.Context) (bool, error) {
	if l.pool == nil {
		return false, errors.New("ha: no database pool")
	}
	ttl := fmt.Sprintf("%d milliseconds", l.ttl.Milliseconds())
	started := time.Now()
	var holder string
	var until time.Time
	err := l.pool.QueryRow(ctx, `
		INSERT INTO ha_leader (name, holder_id, lease_expires_at)
		VALUES ($1, $2, now() + $3::interval)
		ON CONFLICT (name) DO UPDATE
		SET holder_id = EXCLUDED.holder_id,
		    lease_expires_at = EXCLUDED.lease_expires_at,
		    updated_at = now()
		WHERE ha_leader.holder_id = $2
		   OR ha_leader.holder_id = ''
		   OR ha_leader.lease_expires_at < now()
		RETURNING holder_id, lease_expires_at`, leaseName, l.id, ttl).Scan(&holder, &until)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			l.setHeld(false, time.Time{})
			return false, nil
		}
		return false, err
	}
	won := holder == l.id
	if won {
		l.mu.Lock()
		l.deadline = started.Add(l.ttl - l.margin())
		l.mu.Unlock()
		l.setHeld(true, until)
	} else {
		l.setHeld(false, time.Time{})
	}
	return won, nil
}

// margin is how early a leader stops before its lease could expire, to
// absorb clock drift and the time the renew query takes.
func (l *Leader) margin() time.Duration {
	m := l.ttl / 5
	if m > 3*time.Second {
		m = 3 * time.Second
	}
	return m
}

// Release drops the lease if this replica holds it so failover does not wait out the TTL.
func (l *Leader) Release(ctx context.Context) {
	if l.pool != nil {
		_, _ = l.pool.Exec(ctx, `
			UPDATE ha_leader
			SET holder_id = '', lease_expires_at = to_timestamp(0), updated_at = now()
			WHERE name = $1 AND holder_id = $2`, leaseName, l.id)
	}
	l.setHeld(false, time.Time{})
}

// Run renews the lease until ctx is done, then releases it.
func (l *Leader) Run(ctx context.Context) {
	t := time.NewTicker(l.renew)
	defer t.Stop()
	slog.Info("ha lease loop started", "instance", l.id, "ttl", l.ttl.String(), "renew", l.renew.String())
	for {
		select {
		case <-ctx.Done():
			relCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			l.Release(relCtx)
			cancel()
			slog.Info("ha lease loop stopped", "instance", l.id)
			return
		case <-t.C:
			if _, err := l.TryAcquire(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("ha lease acquire failed", "err", err, "instance", l.id)
			}
		}
	}
}

// RunWhenLeader runs fn while this replica holds the lease. Losing the lease
// cancels fn; regaining it starts fn again.
func (l *Leader) RunWhenLeader(ctx context.Context, name string, fn func(context.Context)) {
	for ctx.Err() == nil {
		if !l.IsLeader() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		slog.Info("ha: starting leader work", "name", name, "instance", l.id)
		sub, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-sub.Done():
					return
				case <-t.C:
					if !l.IsLeader() {
						cancel()
						return
					}
				}
			}
		}()
		fn(sub)
		cancel()
		<-done
		if ctx.Err() != nil {
			return
		}
		slog.Info("ha: stopped leader work", "name", name, "instance", l.id)
	}
}

func (l *Leader) setHeld(held bool, until time.Time) {
	l.mu.Lock()
	prev := l.held
	l.held = held
	l.until = until
	cb := l.change
	l.mu.Unlock()
	if prev == held {
		return
	}
	if held {
		slog.Info("ha: acquired scheduler lease", "instance", l.id, "until", until)
	} else {
		slog.Info("ha: lost scheduler lease", "instance", l.id)
	}
	if cb != nil {
		cb(held)
	}
}
