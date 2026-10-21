// Package sla scans for missed-run and late-run conditions and notifies.
// Late-run is notify-only; job timeout_seconds remains the kill switch.
package sla

import (
	"context"
	"log/slog"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// Scanner wakes periodically and enqueues SLA notification events.
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
	slog.Info("sla scanner started", "interval", s.interval.String())
	s.ScanAt(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			slog.Info("sla scanner stopped")
			return
		case <-t.C:
			s.ScanAt(ctx, time.Now())
		}
	}
}

// ScanAt evaluates SLA conditions as of now.
func (s *Scanner) ScanAt(ctx context.Context, now time.Time) {
	s.scanMissed(ctx, now)
	s.scanLate(ctx, now)
}

func (s *Scanner) scanMissed(ctx context.Context, now time.Time) {
	due, err := s.store.DueMissedJobs(ctx, now)
	if err != nil {
		slog.Warn("sla: due missed jobs", "err", err)
		return
	}
	for _, m := range due {
		if err := s.store.EnqueueSLANotify(ctx, store.EventRunMissed, m.Job, m.Run); err != nil {
			slog.Warn("sla: enqueue missed", "job", m.Job.Name, "err", err)
			continue
		}
		if err := s.store.MarkJobMissedAlerted(ctx, m.Job.ID, m.OccurrenceAt); err != nil {
			slog.Warn("sla: mark missed job", "err", err)
		}
	}
	queued, err := s.store.DueMissedQueuedRuns(ctx, now)
	if err != nil {
		slog.Warn("sla: due missed queued", "err", err)
		return
	}
	for _, m := range queued {
		if err := s.store.EnqueueSLANotify(ctx, store.EventRunMissed, m.Job, m.Run); err != nil {
			slog.Warn("sla: enqueue missed queued", "job", m.Job.Name, "err", err)
			continue
		}
		if m.Run != nil {
			if err := s.store.MarkRunMissedAlerted(ctx, m.Run.ID); err != nil {
				slog.Warn("sla: mark missed run", "err", err)
			}
		}
	}
}

func (s *Scanner) scanLate(ctx context.Context, now time.Time) {
	due, err := s.store.DueLateRuns(ctx, now)
	if err != nil {
		slog.Warn("sla: due late runs", "err", err)
		return
	}
	for _, m := range due {
		if err := s.store.EnqueueSLANotify(ctx, store.EventRunLate, m.Job, &m.Run); err != nil {
			slog.Warn("sla: enqueue late", "job", m.Job.Name, "err", err)
			continue
		}
		if err := s.store.MarkRunLateAlerted(ctx, m.Run.ID); err != nil {
			slog.Warn("sla: mark late run", "err", err)
		}
	}
}
