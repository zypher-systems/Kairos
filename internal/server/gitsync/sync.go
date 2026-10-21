package gitsync

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/zypher-systems/kairos/internal/server/ascode"
	"github.com/zypher-systems/kairos/internal/server/store"
)

// Worker polls settings and applies a git tree when due.
type Worker struct {
	store *store.Store
	tick  time.Duration
}

// New builds a worker. Tick is how often settings are re-read.
func New(st *store.Store, tick time.Duration) *Worker {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	return &Worker{store: st, tick: tick}
}

// Run blocks until ctx is done.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.tick)
	defer t.Stop()
	slog.Info("git sync started", "tick", w.tick.String())
	w.maybeSync(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("git sync stopped")
			return
		case <-t.C:
			w.maybeSync(ctx)
		}
	}
}

func (w *Worker) maybeSync(ctx context.Context) {
	cfg, err := w.store.GetSettings(ctx)
	if err != nil {
		slog.Warn("git sync: settings", "err", err)
		return
	}
	if !cfg.GitSyncEnabled || cfg.GitSyncURL == "" {
		return
	}
	interval := time.Duration(cfg.GitSyncIntervalSeconds) * time.Second
	if interval < time.Minute {
		interval = time.Minute
	}
	if cfg.GitSyncLastAt != nil && time.Since(*cfg.GitSyncLastAt) < interval {
		return
	}
	if _, err := SyncOnce(ctx, w.store, false); err != nil {
		slog.Warn("git sync", "err", err)
	}
}

// SyncOnce clones the configured repo and applies YAML under the path.
func SyncOnce(ctx context.Context, st *store.Store, dryRun bool) (ascode.Result, error) {
	cfg, err := st.GetSettings(ctx)
	if err != nil {
		return ascode.Result{}, err
	}
	if cfg.GitSyncURL == "" {
		return ascode.Result{}, fmt.Errorf("git_sync_url is not set")
	}
	token, err := st.RevealGitSyncToken(ctx)
	if err != nil {
		return ascode.Result{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, ascode.SyncTimeout)
	defer cancel()
	dir, sha, err := ascode.ClonePull(cctx, cfg.GitSyncURL, cfg.GitSyncRef, token)
	if err != nil {
		_ = st.PatchGitSyncStatus(ctx, "", err.Error(), "")
		return ascode.Result{}, err
	}
	defer os.RemoveAll(dir)
	raw, err := ascode.CollectYAML(dir, cfg.GitSyncPath)
	if err != nil {
		_ = st.PatchGitSyncStatus(ctx, sha, err.Error(), "")
		return ascode.Result{}, err
	}
	docs, err := ascode.ParseBytes(raw)
	if err != nil {
		_ = st.PatchGitSyncStatus(ctx, sha, err.Error(), "")
		return ascode.Result{}, err
	}
	res, err := ascode.Apply(ctx, st, docs, dryRun)
	summary := summarize(res)
	if err != nil {
		_ = st.PatchGitSyncStatus(ctx, sha, err.Error(), summary)
		return res, err
	}
	errMsg := firstApplyError(res)
	if !dryRun {
		_ = st.PatchGitSyncStatus(ctx, sha, errMsg, summary)
	}
	return res, nil
}

func summarize(res ascode.Result) string {
	var created, updated, unchanged, failed int
	for _, it := range res.Items {
		if it.Error != "" {
			failed++
			continue
		}
		switch it.Action {
		case "create":
			created++
		case "update":
			updated++
		default:
			unchanged++
		}
	}
	return fmt.Sprintf("create=%d update=%d unchanged=%d failed=%d", created, updated, unchanged, failed)
}

func firstApplyError(res ascode.Result) string {
	for _, it := range res.Items {
		if it.Error != "" {
			return it.Kind + " " + it.Name + ": " + it.Error
		}
	}
	return ""
}
