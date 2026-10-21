// Package agents hosts the server-side agent watchdog: liveness marking and
// expired-claim requeueing.
package agents

import (
	"context"
	"log/slog"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// Watchdog periodically marks stale agents offline and returns runs with
// expired leases to the queue.
type Watchdog struct {
	store    *store.Store
	hb       time.Duration
	lease    time.Duration
	interval time.Duration
}

// NewWatchdog builds the watchdog; interval is derived (min(hb, lease)/3).
func NewWatchdog(st *store.Store, heartbeatTimeout, lease time.Duration) *Watchdog {
	interval := heartbeatTimeout / 3
	if lease/3 < interval {
		interval = lease / 3
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Watchdog{store: st, hb: heartbeatTimeout, lease: lease, interval: interval}
}

// Run blocks until ctx is done.
func (w *Watchdog) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	slog.Info("watchdog started", "interval", w.interval.String())
	for {
		select {
		case <-ctx.Done():
			slog.Info("watchdog stopped")
			return
		case <-t.C:
			if err := w.store.MarkStaleAgents(ctx, w.hb); err != nil {
				slog.Warn("mark stale agents", "err", err)
			}
			if n, err := w.store.RequeueExpiredClaims(ctx, time.Now()); err != nil {
				slog.Warn("requeue expired claims", "err", err)
			} else if n > 0 {
				slog.Info("requeued expired claims", "count", n)
			}
			if n, err := w.store.PruneStaleCanceledClaims(ctx, time.Minute); err != nil {
				slog.Warn("prune stale canceled claims", "err", err)
			} else if n > 0 {
				slog.Info("pruned stale canceled claims", "count", n)
			}
		}
	}
}
