// Command kairos-agent is the Kairos remote worker. It registers with a
// Kairos server, polls for claimed work, executes jobs (shell or Docker),
// and streams logs back. Targets linux/amd64 and linux/arm64.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zypher-systems/kairos/internal/agent"
	kversion "github.com/zypher-systems/kairos/internal/version"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("KAIROS_AGENT_DEBUG") == "true" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cfg, err := agent.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	a, err := agent.New(cfg, kversion.Version)
	if err != nil {
		slog.Error("init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := a.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("agent", "err", err)
		os.Exit(1)
	}
}
