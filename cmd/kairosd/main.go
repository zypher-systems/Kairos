// Command kairosd is the Kairos server: REST + WebSocket API, scheduler,
// job queue, and execution orchestration for remote agents and SSH hosts.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zypher-systems/kairos/internal/server/agents"
	"github.com/zypher-systems/kairos/internal/server/approval"
	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/database"
	"github.com/zypher-systems/kairos/internal/server/exec"
	"github.com/zypher-systems/kairos/internal/server/gitsync"
	"github.com/zypher-systems/kairos/internal/server/ha"
	"github.com/zypher-systems/kairos/internal/server/httpapi"
	"github.com/zypher-systems/kairos/internal/server/notify"
	"github.com/zypher-systems/kairos/internal/server/retention"
	"github.com/zypher-systems/kairos/internal/server/scheduler"
	"github.com/zypher-systems/kairos/internal/server/sla"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
	kversion "github.com/zypher-systems/kairos/internal/version"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	level := cfg.SlogLevel()
	if cfg.Development {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
	slog.Info("kairos starting", "version", kversion.Version)

	ctx := context.Background()
	pool, err := database.Connect(ctx, cfg.DBURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	st := store.New(pool, cfg.Secret)
	if err := st.EnsureBootstrapped(ctx); err != nil {
		return err
	}

	hub := ws.New(cfg.PublicURL)
	notifier := notify.New(st, cfg.PublicURL)
	api := httpapi.New(cfg, pool, st, hub, notifier)

	runCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()

	lead := ha.New(pool, "", ha.DefaultTTL, ha.DefaultRenew)
	if _, err := lead.TryAcquire(runCtx); err != nil {
		return err
	}
	if err := lead.BootPolicy(); err != nil {
		return err
	}
	api.SetLeader(lead)
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		lead.Run(runCtx)
	}()
	slog.Info("ha replica", "instance", lead.InstanceID(), "leader", lead.IsLeader())

	go lead.RunWhenLeader(runCtx, "scheduler", scheduler.New(st, hub, cfg.SchedulerTick).Run)
	go lead.RunWhenLeader(runCtx, "pruner", retention.New(st, cfg.DataDir, cfg.RetentionInterval).Run)
	go lead.RunWhenLeader(runCtx, "file-watch", exec.NewWatcher(st, hub, time.Second).Run)
	go lead.RunWhenLeader(runCtx, "watchdog", agents.NewWatchdog(st, cfg.HeartbeatTimeout, cfg.QueueLease).Run)
	go lead.RunWhenLeader(runCtx, "gitsync", gitsync.New(st, 30*time.Second).Run)
	go lead.RunWhenLeader(runCtx, "sla", sla.New(st, 5*time.Second).Run)
	go lead.RunWhenLeader(runCtx, "approval", approval.New(st, 5*time.Second).Run)

	dispatcher := exec.NewDispatcher(st, hub, notifier, cfg.DispatcherConcurrency, time.Second, cfg.QueueLease, cfg.ShutdownGrace, cfg.DataDir)
	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		dispatcher.Run(runCtx)
	}()

	go notifier.Run(runCtx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: agent claims long-poll for 25s and websockets
		// stay open. Idle keep-alives are bounded instead.
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("kairos server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
	}

	// Shutdown order: stop claiming and drain local/SSH runs while the API
	// stays up so agents can still report, then close the listener, then
	// release the leader lease so a successor can take over at once.
	slog.Info("shutting down", "drain", cfg.ShutdownGrace.String())
	stopWorkers()
	<-dispatcherDone
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	<-leaderDone
	return err
}
