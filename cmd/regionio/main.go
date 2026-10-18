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
	cfg.OpsFile = "ops.json"
	cfg.WhitelistFile = "whitelist.json"
	if loaded, err := server.LoadPropertiesFile("server.properties", cfg); err != nil {
		log.Error("failed to load server.properties", "err", err)
		os.Exit(1)
	} else {
		cfg = loaded
	}

	// World seed: -seed flag takes precedence, then REGIONIO_SEED env, then the
	// default (0). Accepted formats: decimal, or "0x" hex. An invalid value is
	// fatal — a wrong seed silently generates a different world than intended.
	seedFlag := flag.Int64("seed", parseSeedEnv(os.Getenv("REGIONIO_SEED"), cfg.WorldSeed, log),
		"world seed (overrides REGIONIO_SEED)")
	port := flag.Int("port", cfg.Port, "TCP listen port")
	worldDir := flag.String("world", cfg.WorldDir, "world directory (empty = in-memory only)")
	maxCache := flag.Int("maxcache", cfg.MaxCachedChunks, "max cached chunks, LRU eviction (0 = unbounded)")
	viewDistance := flag.Int("viewdistance", cfg.MaxViewDistance, "maximum client chunk view radius (2-16)")
	online := flag.Bool("online", cfg.OnlineMode, "enable Mojang authentication and link encryption (online mode)")
	pregenerate := flag.Int("pregenerate", 0, "pregenerate spawn chunk radius before listening (0 = disabled)")
	flag.Parse()
	cfg.WorldSeed = *seedFlag
	cfg.Port = *port
	cfg.WorldDir = *worldDir
	cfg.MaxCachedChunks = *maxCache
	cfg.MaxViewDistance = *viewDistance
	cfg.OnlineMode = *online
	log.Info("using world seed", "seed", cfg.WorldSeed, "worldDir", cfg.WorldDir,
		"maxcache", cfg.MaxCachedChunks, "viewdistance", cfg.MaxViewDistance,
		"online", cfg.OnlineMode, "pregenerate", *pregenerate)

	srv, err := server.New(cfg)
	if err != nil {
		log.Error("failed to create server", "err", err)
		os.Exit(1)
	}

	// Start the chunk autosave loop. It flushes dirty chunks every 30s and does
	// a final SaveAll when the context (cancelled by signal) ends.
	saveCtx, saveStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	srv.SetStopFunc(saveStop)
	autosaveDone := srv.Chunks().StartAutosave(saveCtx, log, 30*time.Second)

	if *pregenerate > 0 {
		start := time.Now()
		log.Info("pregenerating spawn chunks", "radius", *pregenerate)
		if err := srv.Chunks().Pregenerate(saveCtx, 0, 0, *pregenerate); err != nil {
			log.Error("failed to pregenerate spawn chunks", "radius", *pregenerate, "err", err)
			os.Exit(1)
		}
		log.Info("pregeneration complete", "radius", *pregenerate, "elapsed", time.Since(start))
	}

	spawnX, spawnY, spawnZ, _ := srv.WorldSpawn()
	log.Info("world spawn ready", "x", spawnX, "y", spawnY, "z", spawnZ)

	srv.StartSpawning(saveCtx, log)
	go network.RunConsole(saveCtx, os.Stdin, srv, log)

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
	if err := srv.SaveAll(); err != nil {
		log.Error("saving world", "err", err)
	}
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
