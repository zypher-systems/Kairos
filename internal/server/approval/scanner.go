// Package approval times out pending_approval runs whose job gate has elapsed.
package approval

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// Scanner wakes periodically and skips timed-out pending runs.
type Scanner struct {
	store    *store.Store
	interval time.Duration
}

// New builds a scanner. interval defaults to 5s.
func New(st *store.Store, interval time.Duration) *Scanner {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Scanner{store: st, interval: interval}
}

// Run blocks until ctx is done.
func (s *Scanner) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	slog.Info("approval scanner started", "interval", s.interval.String())
	s.ScanAt(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			slog.Info("approval scanner stopped")
			return
		case <-t.C:
			s.ScanAt(ctx, time.Now())
		}
	}
}

// ScanAt skips pending runs whose timeout has elapsed as of now.
func (s *Scanner) ScanAt(ctx context.Context, now time.Time) {
	due, err := s.store.DueApprovalTimeouts(ctx, now)
	if err != nil {
		slog.Warn("approval: due timeouts", "err", err)
		return
	}
	for _, m := range due {
		if _, err := s.store.SkipApprovalTimeout(ctx, m.Run.ID); err != nil {
			if errors.Is(err, store.ErrNotPending) {
				continue
			}
			slog.Warn("approval: skip timeout", "run", m.Run.ID, "err", err)
		}
	}
}
