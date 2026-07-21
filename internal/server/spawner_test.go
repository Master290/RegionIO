package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestSpawnMobNearPlayerUsesSurface(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	session, err := srv.RegisterPlayer(Profile{Name: "Alice", UUID: OfflineUUID("Alice")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPlayerTransform(session, 8.5, 80, 8.5, 0, 0, true)

	if !srv.spawnMobNearPlayer(rand.New(rand.NewSource(1)), 10, 20) {
		t.Fatal("spawnMobNearPlayer returned false")
	}
	entities := srv.Entities().All()
	if len(entities) != 1 {
		t.Fatalf("entities = %d, want 1", len(entities))
	}
	entity := entities[0]
	if entity.Y != world.FlatSurfaceY+1 {
		t.Fatalf("mob Y = %v, want surface Y %d", entity.Y, world.FlatSurfaceY+1)
	}
	distance := math.Hypot(entity.X-8.5, entity.Z-8.5)
	if distance < 15 || distance > 33 {
		t.Fatalf("mob distance = %v, want near player", distance)
	}
}
