// Command regionio starts a RegionIO Minecraft server core.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"regionio/internal/network"
	"regionio/internal/server"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	cfg := server.DefaultConfig()

	// World seed: -seed flag takes precedence, then REGIONIO_SEED env, then the
	// default (0). Accepted formats: decimal, or "0x" hex. An invalid value is
	// fatal — a wrong seed silently generates a different world than intended.
	seedFlag := flag.Int64("seed", parseSeedEnv(os.Getenv("REGIONIO_SEED"), cfg.WorldSeed, log),
		"world seed (overrides REGIONIO_SEED)")
	flag.Parse()
	cfg.WorldSeed = *seedFlag
	log.Info("using world seed", "seed", cfg.WorldSeed)

	srv := server.New(cfg)
	ln := network.NewListener(srv, log)

	// Cancel the context on SIGINT/SIGTERM for a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := ln.ListenAndServe(ctx); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// parseSeedEnv parses the REGIONIO_SEED env var. It returns fallback when the
// variable is empty, and logs+returns fallback when parsing fails (so a typo
// does not silently change the world).
func parseSeedEnv(raw string, fallback int64, log *slog.Logger) int64 {
	if raw == "" {
		return fallback
	}
	// strconv.ParseInt with base 0 handles decimal and "0x" hex prefixes.
	v, err := strconv.ParseInt(raw, 0, 64)
	if err != nil {
		log.Error("invalid REGIONIO_SEED, falling back to default", "raw", raw, "err", err)
		return fallback
	}
	return v
}
