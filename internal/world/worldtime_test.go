package world

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWorldTimeRoundTrip checks the clock survives a restart. Without it the
// sky snaps back to dawn every time the server comes up, however long the world
// has been running.
func TestWorldTimeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreForSeed(dir, 4242)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if gameTime, dayTime := store.WorldTime(); gameTime != 0 || dayTime != 0 {
		t.Fatalf("a new world starts at %d/%d, want 0/0", gameTime, dayTime)
	}
	if err := store.SaveWorldTime(72_000, 18_000); err != nil {
		t.Fatalf("save time: %v", err)
	}
	store.Close()

	reopened, err := NewStoreForSeed(dir, 4242)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	gameTime, dayTime := reopened.WorldTime()
	if gameTime != 72_000 || dayTime != 18_000 {
		t.Errorf("reopened at %d/%d, want 72000/18000", gameTime, dayTime)
	}

	// The seed guard has to survive the rewrite: it is the only thing stopping
	// a world from being regenerated with different terrain.
	if _, err := NewStoreForSeed(dir, 9999); err == nil {
		t.Error("reopening with a different seed was accepted")
	}
}

// TestWorldMetadataWithoutClock accepts a metadata file written before the
// clock was persisted, rather than rejecting the world.
func TestWorldMetadataWithoutClock(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "region"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"format": 1, "seed": 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, worldMetadataFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreForSeed(dir, 7)
	if err != nil {
		t.Fatalf("open a world written before the clock existed: %v", err)
	}
	defer store.Close()
	if gameTime, dayTime := store.WorldTime(); gameTime != 0 || dayTime != 0 {
		t.Errorf("clock read %d/%d from a file that has none, want 0/0", gameTime, dayTime)
	}
	if err := store.SaveWorldTime(10, 20); err != nil {
		t.Fatalf("save time into an older world: %v", err)
	}
	reopened, err := NewStoreForSeed(dir, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if gameTime, dayTime := reopened.WorldTime(); gameTime != 10 || dayTime != 20 {
		t.Errorf("after upgrading the file the clock reads %d/%d, want 10/20", gameTime, dayTime)
	}
}
