// Package retention periodically enforces bounded run history.
package retention

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// Pruner wakes periodically and prunes run history per stored settings.
type Pruner struct {
	store    *store.Store
	dataDir  string
	interval time.Duration
}

// New builds a pruner. dataDir roots the file library for blob pruning.
func New(st *store.Store, dataDir string, interval time.Duration) *Pruner {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Pruner{store: st, dataDir: dataDir, interval: interval}
}

// Run blocks until ctx is done.
func (p *Pruner) Run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	slog.Info("pruner started", "interval", p.interval.String())
	p.prune(ctx) // catch up immediately on boot
	for {
		select {
		case <-ctx.Done():
			slog.Info("pruner stopped")
			return
		case <-t.C:
			p.prune(ctx)
		}
	}
}

func (p *Pruner) prune(ctx context.Context) {
	cfg, err := p.store.GetSettings(ctx)
	if err != nil {
		slog.Warn("prune: load settings", "err", err)
		return
	}
	runs, days := cfg.RetentionRuns, cfg.RetentionDays
	if runs <= 0 {
		runs = 100
	}
	if days <= 0 {
		days = 30
	}
	removed, err := p.store.PruneRuns(ctx, runs, days)
	if err != nil {
		slog.Warn("prune failed", "err", err)
		return
	}
	if removed > 0 {
		slog.Info("pruned runs", "removed", removed, "keep", runs, "days", days)
	}
	if n, err := p.store.PruneHousekeeping(ctx); err != nil {
		slog.Warn("prune housekeeping failed", "err", err)
	} else if n > 0 {
		slog.Info("pruned housekeeping rows", "removed", n)
	}
	if cfg.LibraryMaxAgeDays > 0 {
		prunedFiles, err := p.store.PruneFilesOlderThan(ctx, cfg.LibraryMaxAgeDays)
		if err != nil {
			slog.Warn("prune files failed", "err", err)
			return
		}
		for _, path := range prunedFiles {
			if err := os.Remove(filepath.Join(p.dataDir, path)); err != nil {
				slog.Warn("prune file blob", "path", path, "err", err)
			}
		}
		if len(prunedFiles) > 0 {
			slog.Info("pruned library files", "removed", len(prunedFiles), "max_age_days", cfg.LibraryMaxAgeDays)
		}
	}
}
