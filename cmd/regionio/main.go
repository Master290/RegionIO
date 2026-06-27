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
	"time"

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
	worldDir := flag.String("world", cfg.WorldDir, "world directory (empty = in-memory only)")
	maxCache := flag.Int("maxcache", cfg.MaxCachedChunks, "max cached chunks, LRU eviction (0 = unbounded)")
	flag.Parse()
	cfg.WorldSeed = *seedFlag
	cfg.WorldDir = *worldDir
	cfg.MaxCachedChunks = *maxCache
	log.Info("using world seed", "seed", cfg.WorldSeed, "worldDir", cfg.WorldDir, "maxcache", cfg.MaxCachedChunks)

	srv, err := server.New(cfg)
	if err != nil {
		log.Error("failed to create server", "err", err)
		os.Exit(1)
	}

	// Start the chunk autosave loop. It flushes dirty chunks every 30s and does
	// a final SaveAll when the context (cancelled by signal) ends.
	saveCtx, saveStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	autosaveDone := srv.Chunks().StartAutosave(saveCtx, log, 30*time.Second)

	srv.StartSpawning()

	ln := network.NewListener(srv, log)

	// ListenAndServe blocks until the listener stops (on the same signals).
	// The autosave context is separate so the final flush runs after the
	// listener exits; stop it here to trigger that final SaveAll, then wait for
	// the saver to finish before releasing the store file handles.
	if err := ln.ListenAndServe(saveCtx); err != nil {
		log.Error("server stopped", "err", err)
	}
	saveStop()
	<-autosaveDone
	if srv.Store() != nil {
		srv.Store().Close()
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
