package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"regionio/internal/world"
)

func TestPlayerRegistrySupportsFourPlayersAndEnforcesLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxPlayers = 4
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	var sends atomic.Int32
	var sessions []*PlayerSession
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave"} {
		profile := Profile{Name: name, UUID: OfflineUUID(name)}
		session, err := srv.RegisterPlayer(profile, func(int32, []byte) error {
			sends.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		sessions = append(sessions, session)
	}
	if got := srv.PlayerCount(); got != 4 {
		t.Fatalf("player count = %d, want 4", got)
	}
	if _, err := srv.RegisterPlayer(Profile{Name: "Eve", UUID: OfflineUUID("Eve")}, nil); !errors.Is(err, ErrServerFull) {
		t.Fatalf("fifth player error = %v, want ErrServerFull", err)
	}

	srv.Broadcast(1, []byte("packet"))
	if got := sends.Load(); got != 4 {
		t.Fatalf("broadcast sends = %d, want 4", got)
	}
	for _, session := range sessions {
		srv.UnregisterPlayer(session)
	}
	if got := srv.PlayerCount(); got != 0 {
		t.Fatalf("player count after disconnect = %d, want 0", got)
	}
}

func TestConcurrentFourPlayerTransformsAndChunkBroadcast(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxPlayers = 4
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	var sends [4]atomic.Int32
	sessions := make([]*PlayerSession, 4)
	for i, name := range []string{"Alice", "Bob", "Carol", "Dave"} {
		i := i
		sessions[i], err = srv.RegisterPlayer(Profile{Name: name, UUID: OfflineUUID(name)}, func(int32, []byte) error {
			sends[i].Add(1)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		srv.SetPlayerViewDistance(sessions[i], 2)
	}

	var wg sync.WaitGroup
	for i, session := range sessions {
		i, session := i, session
		wg.Add(1)
		go func() {
			defer wg.Done()
			for step := 0; step < 500; step++ {
				srv.SetPlayerTransform(session, float64(i*16+step), 80, float64(-step), float32(step%360), 10, step%2 == 0)
				_ = srv.PlayerSnapshots()
				srv.BroadcastChunk(int32(step>>4), int32(-step>>4), 1, nil)
			}
		}()
	}
	wg.Wait()
	if got := len(srv.PlayerSnapshots()); got != 4 {
		t.Fatalf("snapshot count = %d, want 4", got)
	}

	for i, session := range sessions {
		x := float64(i * 160)
		srv.SetPlayerTransform(session, x, 80, 0, 0, 0, true)
		before := sends[i].Load()
		srv.BroadcastChunk(0, 0, 2, nil)
		got := sends[i].Load() - before
		if i == 0 && got != 1 {
			t.Fatalf("near player received %d packets, want 1", got)
		}
		if i > 0 && got != 0 {
			t.Fatalf("far player %d received %d packets, want 0", i, got)
		}
	}
}

func TestPlayerRegistryRejectsDuplicateName(t *testing.T) {
	cfg := DefaultConfig()
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	first := Profile{Name: "Alice", UUID: OfflineUUID("Alice")}
	if _, err := srv.RegisterPlayer(first, nil); err != nil {
		t.Fatal(err)
	}
	duplicate := Profile{Name: "ALICE", UUID: OfflineUUID("ALICE")}
	if _, err := srv.RegisterPlayer(duplicate, nil); !errors.Is(err, ErrDuplicatePlayer) {
		t.Fatalf("duplicate error = %v, want ErrDuplicatePlayer", err)
	}
}

func TestServerRejectsNegativeCacheLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxCachedChunks = -1
	if _, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat)); err == nil {
		t.Fatal("negative cache limit was accepted")
	}
}

func TestNewUsesProductionBatchGenerator(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cfg.WorldSeed = 12345
	cfg.MaxCachedChunks = 64
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Chunks().PreloadErrContext(context.Background(), 0, 0); err != nil {
		t.Fatal(err)
	}
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			if !srv.Chunks().IsChunkLoaded(cx, cz) {
				t.Fatalf("production batch did not publish neighbor (%d,%d)", cx, cz)
			}
		}
	}
}
